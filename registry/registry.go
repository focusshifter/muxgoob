package registry

import (
	"reflect"
	"strings"
	"log"
	"io/ioutil"

	"gopkg.in/yaml.v2"

	"github.com/tucnak/telebot"
	"github.com/asdine/storm"
)

// Plugins contains a list of loaded plugins
var Plugins = map[string]MuxPlugin{}
var Bot *telebot.Bot
var Config Configuration

// MuxPlugin is a basic plugin interface
type MuxPlugin interface {
	Start(sharedDb *storm.DB)
	Process(message telebot.Message)
}

// Configuration stores a struct loaded from config.yml
type Configuration struct {
    TelegramKey string `yaml:"telegram_key"`
    ReplyTechLink string `yaml:"reply_tech_link"`
	ReplyUkranians string `yaml:"reply_ukranians"`
	UkranianUsernames string `yaml:"ukranian_usernames"`
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
}

// RegisterPlugin
func RegisterPlugin(p MuxPlugin) {
	key := strings.TrimPrefix(reflect.TypeOf(p).String(), "*")

	Plugins[key] = p
}
