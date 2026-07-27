package registry

import (
	"context"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/tucnak/telebot"
)

// Plugins contains a list of loaded plugins
var Plugins = map[string]MuxPlugin{}
var Bot *BotWrapper
var Config Configuration

var (
	dispatchMu       sync.Mutex
	dispatchWG       sync.WaitGroup
	dispatchStopping bool
)

// MuxPlugin is a basic plugin interface
type MuxPlugin interface {
	Start(interface{})
	Process(message *telebot.Message)
}

// ShutdownPlugin is optionally implemented by plugins that own background workers.
type ShutdownPlugin interface {
	Shutdown(context.Context)
}

type Trigger struct {
	Usernames []string
	Chance    int
	Reply     string
}

type NametriggerPluginConfig struct {
	Triggers []Trigger `yaml:"triggers"`
}

// Configuration stores a struct loaded from config.yml
type BirthdayConfig struct {
	ChatID int64             `yaml:"chat_id"`
	Users  map[string]string `yaml:"users"`
}

type TwitchStreamConfig struct {
	ChatID          int64    `yaml:"chat_id"`
	TwitchUsernames []string `yaml:"twitch_usernames"`
}

type ChatGptConfigPerChat struct {
	ChatID       int64  `yaml:"chat_id"`
	SystemPrompt string `yaml:"system_prompt"`
}

// SelfPromptConfig holds configuration for the self-updating prompt plugin
type SelfPromptConfig struct {
	Enabled         bool    `yaml:"enabled"`
	MessageInterval int64   `yaml:"message_interval"`
	DisabledChats   []int64 `yaml:"disabled_chats"`
}

// SpotifyConfig holds configuration for the Spotify plugin
type SpotifyConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type Configuration struct {
	TelegramKey                string                  `yaml:"telegram_key"`
	ReplyTechLink              string                  `yaml:"reply_tech_link"`
	NametriggerConfig          NametriggerPluginConfig `yaml:"nametrigger"`
	Birthdays                  []BirthdayConfig        `yaml:"birthdays"`
	TimeZone                   string                  `yaml:"time_zone"`
	TimeLoc                    *time.Location
	DupeIgnoredDomains         []string               `yaml:"dupe_ignored_domains"`
	TwitchAPIKey               string                 `yaml:"twitch_api_key"`
	TwitchAPISecret            string                 `yaml:"twitch_api_secret"`
	TwitchStreams              []TwitchStreamConfig   `yaml:"twitch_streams"`
	OpenaiApiKey               string                 `yaml:"openai_api_key"`
	ChatGptUseHistory          bool                   `yaml:"chat_gpt_use_history"`
	ChatGptSystemPrompt        string                 `yaml:"chat_gpt_system_prompt"`
	ChatGptConfigPerChat       []ChatGptConfigPerChat `yaml:"chat_gpt_config_per_chat"`
	ChatGptUserPrompt          string                 `yaml:"chat_gpt_user_prompt"`
	ChatGptHistoryDepth        int                    `yaml:"chat_gpt_history_depth"`
	OpenrouterApiKey           string                 `yaml:"openrouter_api_key"`
	OwnerUsername              string                 `yaml:"owner_username"`
	AiProvider                 string                 `yaml:"ai_provider"`
	AiModel                    string                 `yaml:"ai_model"`
	ImageAiModel               string                 `yaml:"image_ai_model"`
	ImageAiProvider            string                 `yaml:"image_ai_provider"`
	ImagePromptProvider        string                 `yaml:"image_prompt_provider"`
	ImagePromptModel           string                 `yaml:"image_prompt_model"`
	ImagePromptMode            string                 `yaml:"image_prompt_mode"`
	ImageMetadataEnabled       *bool                  `yaml:"image_metadata_enabled"`
	ImageMetadataMaxPerMinute  int                    `yaml:"image_metadata_max_per_minute"`
	SelfPromptConfig           SelfPromptConfig       `yaml:"selfprompt"`
	SpotifyConfig              SpotifyConfig          `yaml:"spotify"`
	SpotifyReviewPrompt        string                 `yaml:"spotify_review_prompt"`
	SpotifyReviewMicroblogAuth string                 `yaml:"spotify_review_microblog_auth"`
}

// LoadConfig reads configuration into registry.Config
func LoadConfig(configPath string) {
	source, err := os.ReadFile(configPath)

	if err != nil {
		log.Fatal(err)
	}

	err = yaml.Unmarshal(source, &Config)
	if err != nil {
		log.Fatal(err)
	}

	loc, err := time.LoadLocation(Config.TimeZone)
	if err != nil {
		log.Printf("Invalid time_zone %q; using local timezone %q: %v", Config.TimeZone, time.Local, err)
		loc = time.Local
	}
	Config.TimeLoc = loc

	log.Printf("Loaded config from %s", configPath)
}

// RegisterPlugin
func RegisterPlugin(p MuxPlugin) {
	key := strings.TrimPrefix(reflect.TypeOf(p).String(), "*")

	log.Printf("Registered plugin: %v", key)

	Plugins[key] = p
}

// DispatchMessage runs all registered plugins for an incoming or scheduled message.
// Plugins run independently so a slow plugin does not block the rest of the bot.
func DispatchMessage(message *telebot.Message) {
	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	if dispatchStopping {
		return
	}
	for _, plugin := range Plugins {
		dispatchWG.Add(1)
		go func(plugin MuxPlugin) {
			defer dispatchWG.Done()
			plugin.Process(message)
		}(plugin)
	}
}

// Shutdown stops plugins that own background workers and waits for in-flight
// message handlers before databases are closed.
func Shutdown(ctx context.Context) {
	dispatchMu.Lock()
	dispatchStopping = true
	plugins := make([]MuxPlugin, 0, len(Plugins))
	for _, plugin := range Plugins {
		plugins = append(plugins, plugin)
	}
	dispatchMu.Unlock()

	for _, plugin := range plugins {
		if stoppable, ok := plugin.(ShutdownPlugin); ok {
			stoppable.Shutdown(ctx)
		}
	}

	done := make(chan struct{})
	go func() {
		dispatchWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Printf("[registry] Drained in-flight message handlers")
	case <-ctx.Done():
		log.Printf("[registry] Shutdown deadline reached with message handlers still running: %v", ctx.Err())
	}
}
