package logwrite

import (
	"github.com/asdine/storm"
	"github.com/tucnak/telebot"
)

// LogWritePlugin is now just a wrapper around LogWriteDualPlugin
type LogWritePlugin struct {
	dual *LogWriteDualPlugin
}

func (p *LogWritePlugin) Start(sharedDb *storm.DB) {
	p.dual = &LogWriteDualPlugin{}
	p.dual.Start(sharedDb)
}

func (p *LogWritePlugin) Process(message *telebot.Message) {
	p.dual.Process(message)
}
