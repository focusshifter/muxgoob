package registry

import (
	"log"
	"os"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/tucnak/telebot"

	"github.com/davecgh/go-spew/spew"
)

// Plugins contains a list of loaded plugins
var Plugins = map[string]MuxPlugin{}
var Bot *BotWrapper
var Config Configuration

// MuxPlugin is a basic plugin interface
type MuxPlugin interface {
	Start(interface{})
	Process(message *telebot.Message)
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

type Configuration struct {
	TelegramKey          string                  `yaml:"telegram_key"`
	ReplyTechLink        string                  `yaml:"reply_tech_link"`
	NametriggerConfig    NametriggerPluginConfig `yaml:"nametrigger"`
	Birthdays            []BirthdayConfig        `yaml:"birthdays"`
	TimeZone             string                  `yaml:"time_zone"`
	TimeLoc              *time.Location
	DupeIgnoredDomains   []string               `yaml:"dupe_ignored_domains"`
	TwitchAPIKey         string                 `yaml:"twitch_api_key"`
	TwitchAPISecret      string                 `yaml:"twitch_api_secret"`
	TwitchStreams        []TwitchStreamConfig   `yaml:"twitch_streams"`
	OpenaiApiKey         string                 `yaml:"openai_api_key"`
	ChatGptUseHistory    bool                   `yaml:"chat_gpt_use_history"`
	ChatGptSystemPrompt  string                 `yaml:"chat_gpt_system_prompt"`
	ChatGptConfigPerChat []ChatGptConfigPerChat `yaml:"chat_gpt_config_per_chat"`
	ChatGptUserPrompt    string                 `yaml:"chat_gpt_user_prompt"`
	ChatGptHistoryDepth  int                    `yaml:"chat_gpt_history_depth"`
	OpenrouterApiKey     string                 `yaml:"openrouter_api_key"`
	OwnerUsername        string                 `yaml:"owner_username"`
	AiProvider           string                 `yaml:"ai_provider"`
	AiModel              string                 `yaml:"ai_model"`
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

	loc, _ := time.LoadLocation(Config.TimeZone)
	Config.TimeLoc = loc

	spew.Dump(Config)
}

// RegisterPlugin
func RegisterPlugin(p MuxPlugin) {
	key := strings.TrimPrefix(reflect.TypeOf(p).String(), "*")

	log.Printf("Registered plugin: %v", key)

	Plugins[key] = p
}
