package registry

import (
	"io/ioutil"
	"log"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/davecgh/go-spew/spew"
)

// Plugins contains a list of loaded plugins
var Plugins = map[string]MuxPlugin{}
var Bot *telebot.Bot
var Config Configuration

// MuxPlugin is a basic plugin interface
type MuxPlugin interface {
	Start(sharedDb *storm.DB)
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
type Configuration struct {
	TelegramKey        string                  `yaml:"telegram_key"`
	ReplyTechLink      string                  `yaml:"reply_tech_link"`
	NametriggerConfig  NametriggerPluginConfig `yaml:"nametrigger"`
	Birthdays          map[string]string       `yaml:"birthdays"`
	TimeZone           string                  `yaml:"time_zone"`
	TimeLoc            *time.Location
	DupeIgnoredDomains []string `yaml:"dupe_ignored_domains"`
	TwitchAPIKey       string   `yaml:"twitch_api_key"`
	TwitchStreams      []string `yaml:"twitch_streams"`
}

// LoadConfig reads configuration into registry.Config
func LoadConfig(configPath string) {
	source, err := ioutil.ReadFile(configPath)

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
