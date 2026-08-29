package registry

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"

	"github.com/tucnak/telebot"
)

var ErrDispatcherClosed = errors.New("dispatcher is shutting down")

type pluginSource func() map[string]MuxPlugin

type chatQueue struct {
	messages  []*telebot.Message
	running   bool
	scheduled bool
}

// Dispatcher preserves FIFO admission order within each chat, fairly schedules
// ready chats over a fixed worker pool, and bounds both pending messages and
// active plugin calls.
type Dispatcher struct {
	mu              sync.Mutex
	plugins         pluginSource
	queues          map[int64]*chatQueue
	ready           chan int64
	pluginSemaphore chan struct{}
	maxPending      int
	maxPerChat      int
	pending         int
	closing         bool
	capacityChanged chan struct{}
	jobs            sync.WaitGroup
	workers         sync.WaitGroup
	stopWorkers     chan struct{}
	stopOnce        sync.Once
}

func NewDispatcher(plugins pluginSource, maxConcurrent, queueSize int) *Dispatcher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}
	d := &Dispatcher{
		plugins:         plugins,
		queues:          make(map[int64]*chatQueue),
		ready:           make(chan int64, queueSize),
		pluginSemaphore: make(chan struct{}, maxConcurrent),
		maxPending:      queueSize,
		maxPerChat:      queueSize,
		capacityChanged: make(chan struct{}),
		stopWorkers:     make(chan struct{}),
	}
	for i := 0; i < maxConcurrent; i++ {
		d.workers.Add(1)
		go d.worker()
	}
	return d
}

func (d *Dispatcher) Dispatch(message *telebot.Message) error {
	return d.DispatchContext(context.Background(), message)
}

// DispatchContext applies backpressure instead of dropping work. A successful
// return guarantees that Shutdown will wait for this message.
func (d *Dispatcher) DispatchContext(ctx context.Context, message *telebot.Message) error {
	if message == nil || message.Chat == nil {
		return errors.New("message and chat are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		d.mu.Lock()
		if d.closing {
			d.mu.Unlock()
			return ErrDispatcherClosed
		}
		queue := d.queues[message.Chat.ID]
		if queue == nil {
			queue = &chatQueue{}
			d.queues[message.Chat.ID] = queue
		}
		if d.pending < d.maxPending && len(queue.messages) < d.maxPerChat {
			queue.messages = append(queue.messages, message)
			d.pending++
			d.jobs.Add(1)
			schedule := !queue.running && !queue.scheduled
			if schedule {
				queue.scheduled = true
			}
			d.mu.Unlock()
			if schedule {
				d.ready <- message.Chat.ID
			}
			return nil
		}
		wait := d.capacityChanged
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
		}
	}
}

func (d *Dispatcher) worker() {
	defer d.workers.Done()
	for {
		select {
		case chatID := <-d.ready:
			message, ok := d.take(chatID)
			if !ok {
				continue
			}
			d.processMessage(message)
			d.finish(chatID)
		case <-d.stopWorkers:
			return
		}
	}
}

func (d *Dispatcher) take(chatID int64) (*telebot.Message, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	queue := d.queues[chatID]
	if queue == nil || queue.running || len(queue.messages) == 0 {
		return nil, false
	}
	queue.scheduled = false
	queue.running = true
	message := queue.messages[0]
	queue.messages = queue.messages[1:]
	return message, true
}

func (d *Dispatcher) finish(chatID int64) {
	d.mu.Lock()
	queue := d.queues[chatID]
	schedule := false
	if queue != nil {
		queue.running = false
		if len(queue.messages) > 0 {
			queue.scheduled = true
			schedule = true
		} else {
			delete(d.queues, chatID)
		}
	}
	d.pending--
	d.notifyCapacityLocked()
	d.jobs.Done()
	d.mu.Unlock()
	if schedule {
		d.ready <- chatID
	}
}

func (d *Dispatcher) notifyCapacityLocked() {
	close(d.capacityChanged)
	d.capacityChanged = make(chan struct{})
}

func (d *Dispatcher) processMessage(message *telebot.Message) {
	plugins := d.plugins()
	var pluginWG sync.WaitGroup
	for name, plugin := range plugins {
		name, plugin := name, plugin
		if plugin == nil {
			continue
		}
		pluginWG.Add(1)
		go func() {
			defer pluginWG.Done()
			d.pluginSemaphore <- struct{}{}
			defer func() { <-d.pluginSemaphore }()
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("[registry] Plugin panic plugin=%s chat=%d message=%d panic=%v\n%s", name, message.Chat.ID, message.ID, recovered, debug.Stack())
				}
			}()
			plugin.Process(message)
		}()
	}
	pluginWG.Wait()
}

func (d *Dispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if !d.closing {
		d.closing = true
		d.notifyCapacityLocked()
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.jobs.Wait()
		close(done)
	}()

	select {
	case <-done:
		d.stopOnce.Do(func() { close(d.stopWorkers) })
		d.workers.Wait()
		return nil
	case <-ctx.Done():
		return fmt.Errorf("drain dispatcher: %w", ctx.Err())
	}
}
