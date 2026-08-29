package registry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tucnak/telebot"
)

type testPlugin struct {
	process func(*telebot.Message)
}

func (p *testPlugin) Start(interface{})                {}
func (p *testPlugin) Process(message *telebot.Message) { p.process(message) }

func TestDispatcherPreservesOrderWithinChat(t *testing.T) {
	var mu sync.Mutex
	var order []int
	d := NewDispatcher(func() map[string]MuxPlugin {
		return map[string]MuxPlugin{"test": &testPlugin{process: func(message *telebot.Message) {
			if message.ID == 1 {
				time.Sleep(30 * time.Millisecond)
			}
			mu.Lock()
			order = append(order, message.ID)
			mu.Unlock()
		}}}
	}, 4, 8)

	for i := 1; i <= 3; i++ {
		if err := d.Dispatch(&telebot.Message{ID: i, Chat: &telebot.Chat{ID: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestDispatcherRunsDifferentChatsConcurrentlyAndBoundsPlugins(t *testing.T) {
	var current atomic.Int32
	var maximum atomic.Int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	plugin := &testPlugin{process: func(*telebot.Message) {
		n := current.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-release
		current.Add(-1)
	}}
	d := NewDispatcher(func() map[string]MuxPlugin {
		return map[string]MuxPlugin{"a": plugin}
	}, 2, 4)

	if err := d.Dispatch(&telebot.Message{ID: 1, Chat: &telebot.Chat{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Dispatch(&telebot.Message{ID: 1, Chat: &telebot.Chat{ID: 2}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("different chats did not run concurrently")
		}
	}
	if got := maximum.Load(); got > 2 {
		t.Fatalf("concurrency exceeded limit: %d", got)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherBackpressureHonorsContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	d := NewDispatcher(func() map[string]MuxPlugin {
		return map[string]MuxPlugin{"slow": &testPlugin{process: func(*telebot.Message) { close(started); <-release }}}
	}, 1, 1)
	if err := d.Dispatch(&telebot.Message{ID: 1, Chat: &telebot.Chat{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := d.DispatchContext(ctx, &telebot.Message{ID: 2, Chat: &telebot.Chat{ID: 1}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context backpressure error, got %v", err)
	}
	close(release)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := d.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherRejectsAfterShutdownAndContainsPanic(t *testing.T) {
	processed := make(chan int, 1)
	d := NewDispatcher(func() map[string]MuxPlugin {
		return map[string]MuxPlugin{
			"panic": &testPlugin{process: func(*telebot.Message) { panic("boom") }},
			"ok":    &testPlugin{process: func(message *telebot.Message) { processed <- message.ID }},
		}
	}, 2, 4)
	if err := d.Dispatch(&telebot.Message{ID: 9, Chat: &telebot.Chat{ID: 1}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-processed:
		if id != 9 {
			t.Fatalf("unexpected id %d", id)
		}
	default:
		t.Fatal("healthy plugin did not run")
	}
	if err := d.Dispatch(&telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 1}}); err == nil {
		t.Fatal("expected dispatch rejection after shutdown")
	}
}
