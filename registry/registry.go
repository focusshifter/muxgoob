package registry

import (
	"reflect"
	"strings"

	"github.com/tucnak/telebot"
	"github.com/asdine/storm"
)

// Plugins contains a list of loaded plugins
var Plugins = map[string]MuxPlugin{}
var Bot *telebot.Bot

// MuxPlugin is a basic plugin interface
type MuxPlugin interface {
	Start(sharedDb *storm.DB)
	Process(message telebot.Message)
}

// RegisterPlugin
func RegisterPlugin(p MuxPlugin) {
	key := strings.TrimPrefix(reflect.TypeOf(p).String(), "*")

	Plugins[key] = p
}
