package cron

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	robfigcron "github.com/robfig/cron/v3"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

var (
	addCommand        = regexp.MustCompile(`^!cron\s+add\s+(-?\d+)\s+"([^"]+)"\s+([A-Za-z0-9_-]+)\s+(.+?)\s*$`)
	removeCommand     = regexp.MustCompile(`^!cron\s+remove\s+(-?\d+)\s+([A-Za-z0-9_-]+)\s*$`)
	rescheduleCommand = regexp.MustCompile(`^!cron\s+reschedule\s+(-?\d+)\s+([A-Za-z0-9_-]+)\s+"([^"]+)"\s*$`)
	updateCommand     = regexp.MustCompile(`^!cron\s+update\s+(-?\d+)\s+([A-Za-z0-9_-]+)\s+(.+?)\s*$`)
	listCommand       = regexp.MustCompile(`^!cron\s+list(?:\s+(-?\d+))?\s*$`)
)

// CronPlugin persists owner-managed scheduled bot commands and dispatches them
// as messages from the configured bot owner in the target chat.
type CronPlugin struct {
	scheduler *scheduler
}

type cronJob struct {
	ChatID     int64
	Alias      string
	Expression string
	Command    string
}

type scheduler struct {
	cron    *robfigcron.Cron
	entries map[string]robfigcron.EntryID
	mu      sync.Mutex
}

func init() {
	registry.RegisterPlugin(&CronPlugin{})
}

func (p *CronPlugin) Start(interface{}) {
	if database.DB == nil {
		log.Printf("[cron] Database is not initialized, scheduler disabled")
		return
	}

	p.scheduler = newScheduler(configuredLocation())
	if err := p.scheduler.loadJobs(); err != nil {
		log.Printf("[cron] Failed to load jobs: %v", err)
	}
	p.scheduler.cron.Start()
}

func (p *CronPlugin) Shutdown(ctx context.Context) {
	if p == nil || p.scheduler == nil || p.scheduler.cron == nil {
		return
	}
	stopped := p.scheduler.cron.Stop()
	select {
	case <-stopped.Done():
		log.Printf("[cron] Scheduler stopped")
	case <-ctx.Done():
		log.Printf("[cron] Scheduler did not stop before shutdown deadline: %v", ctx.Err())
	}
}

func newPluginForTest() *CronPlugin {
	return &CronPlugin{scheduler: newScheduler(configuredLocation())}
}

func newScheduler(loc *time.Location) *scheduler {
	return &scheduler{
		cron:    robfigcron.New(robfigcron.WithLocation(loc)),
		entries: make(map[string]robfigcron.EntryID),
	}
}

func configuredLocation() *time.Location {
	if registry.Config.TimeLoc != nil {
		return registry.Config.TimeLoc
	}
	return time.Local
}

func (p *CronPlugin) Process(message *telebot.Message) {
	if message == nil || message.Chat == nil || message.Sender == nil || !strings.HasPrefix(message.Text, "!cron") {
		return
	}
	if !messageSenderIsBotOwner(message) {
		return
	}
	if p.scheduler == nil {
		p.scheduler = newScheduler(configuredLocation())
	}

	switch {
	case addCommand.MatchString(message.Text):
		p.handleAdd(message, addCommand.FindStringSubmatch(message.Text))
	case removeCommand.MatchString(message.Text):
		p.handleRemove(message, removeCommand.FindStringSubmatch(message.Text))
	case rescheduleCommand.MatchString(message.Text):
		p.handleReschedule(message, rescheduleCommand.FindStringSubmatch(message.Text))
	case updateCommand.MatchString(message.Text):
		p.handleUpdate(message, updateCommand.FindStringSubmatch(message.Text))
	case listCommand.MatchString(message.Text):
		p.handleList(message, listCommand.FindStringSubmatch(message.Text))
	default:
		p.replyUsage(message)
	}
}

func (p *CronPlugin) handleAdd(message *telebot.Message, parts []string) {
	job, err := parseJob(parts[1], parts[3], parts[2], parts[4])
	if err != nil {
		p.reply(message, err.Error())
		return
	}
	if _, err := parseSchedule(job.Expression, configuredLocation()); err != nil {
		p.reply(message, "Invalid cron expression: "+err.Error())
		return
	}

	now := time.Now().Unix()
	_, err = database.DB.Exec(`
		INSERT INTO cron_jobs (chat_id, alias, expression, command, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, alias) DO UPDATE SET
			expression = excluded.expression,
			command = excluded.command,
			updated_at = excluded.updated_at`, job.ChatID, job.Alias, job.Expression, job.Command, now, now)
	if err != nil {
		p.reply(message, "Failed to save cron job: "+err.Error())
		return
	}
	if err := p.scheduler.addOrReplace(job); err != nil {
		p.reply(message, "Failed to schedule cron job: "+err.Error())
		return
	}
	p.reply(message, fmt.Sprintf("Cron job %q added for chat %d (%s, %s).", job.Alias, job.ChatID, job.Expression, job.Command))
}

func (p *CronPlugin) handleRemove(message *telebot.Message, parts []string) {
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		p.reply(message, "Invalid chat_id")
		return
	}
	alias := parts[2]
	result, err := database.DB.Exec(`DELETE FROM cron_jobs WHERE chat_id = ? AND alias = ?`, chatID, alias)
	if err != nil {
		p.reply(message, "Failed to remove cron job: "+err.Error())
		return
	}
	rows, err := result.RowsAffected()
	if err != nil {
		p.reply(message, "Failed to remove cron job: "+err.Error())
		return
	}
	if rows == 0 {
		p.reply(message, fmt.Sprintf("Cron job %q was not found for chat %d.", alias, chatID))
		return
	}
	p.scheduler.remove(jobKey(chatID, alias))
	p.reply(message, fmt.Sprintf("Cron job %q removed from chat %d.", alias, chatID))
}

func (p *CronPlugin) handleReschedule(message *telebot.Message, parts []string) {
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		p.reply(message, "Invalid chat_id")
		return
	}
	alias, expression := parts[2], parts[3]
	if _, err := parseSchedule(expression, configuredLocation()); err != nil {
		p.reply(message, "Invalid cron expression: "+err.Error())
		return
	}
	job, err := loadJob(chatID, alias)
	if err == sql.ErrNoRows {
		p.reply(message, fmt.Sprintf("Cron job %q was not found for chat %d.", alias, chatID))
		return
	}
	if err != nil {
		p.reply(message, "Failed to load cron job: "+err.Error())
		return
	}
	job.Expression = expression
	if _, err := database.DB.Exec(`UPDATE cron_jobs SET expression = ?, updated_at = ? WHERE chat_id = ? AND alias = ?`, expression, time.Now().Unix(), chatID, alias); err != nil {
		p.reply(message, "Failed to reschedule cron job: "+err.Error())
		return
	}
	if err := p.scheduler.addOrReplace(job); err != nil {
		p.reply(message, "Failed to reschedule cron job: "+err.Error())
		return
	}
	p.reply(message, fmt.Sprintf("Cron job %q rescheduled for chat %d (%s).", alias, chatID, expression))
}

func (p *CronPlugin) handleUpdate(message *telebot.Message, parts []string) {
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		p.reply(message, "Invalid chat_id")
		return
	}
	alias, command := parts[2], strings.TrimSpace(parts[3])
	if command == "" {
		p.reply(message, "Command must not be empty")
		return
	}
	job, err := loadJob(chatID, alias)
	if err == sql.ErrNoRows {
		p.reply(message, fmt.Sprintf("Cron job %q was not found for chat %d.", alias, chatID))
		return
	}
	if err != nil {
		p.reply(message, "Failed to load cron job: "+err.Error())
		return
	}
	job.Command = command
	if _, err := database.DB.Exec(`UPDATE cron_jobs SET command = ?, updated_at = ? WHERE chat_id = ? AND alias = ?`, command, time.Now().Unix(), chatID, alias); err != nil {
		p.reply(message, "Failed to update cron job: "+err.Error())
		return
	}
	if err := p.scheduler.addOrReplace(job); err != nil {
		p.reply(message, "Failed to update cron job: "+err.Error())
		return
	}
	p.reply(message, fmt.Sprintf("Cron job %q updated for chat %d.", alias, chatID))
}

func (p *CronPlugin) handleList(message *telebot.Message, parts []string) {
	query := `SELECT chat_id, alias, expression, command FROM cron_jobs`
	args := []interface{}{}
	if parts[1] != "" {
		chatID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			p.reply(message, "Invalid chat_id")
			return
		}
		query += ` WHERE chat_id = ?`
		args = append(args, chatID)
	}
	query += ` ORDER BY chat_id, alias`

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		p.reply(message, "Failed to list cron jobs: "+err.Error())
		return
	}
	defer rows.Close()

	lines := []string{"Cron jobs (" + configuredLocation().String() + "):"}
	for rows.Next() {
		var job cronJob
		if err := rows.Scan(&job.ChatID, &job.Alias, &job.Expression, &job.Command); err != nil {
			p.reply(message, "Failed to list cron jobs: "+err.Error())
			return
		}
		lines = append(lines, fmt.Sprintf("%d / %s — %q → %s", job.ChatID, job.Alias, job.Expression, job.Command))
	}
	if err := rows.Err(); err != nil {
		p.reply(message, "Failed to list cron jobs: "+err.Error())
		return
	}
	if len(lines) == 1 {
		lines = append(lines, "No cron jobs configured.")
	}
	p.reply(message, strings.Join(lines, "\n"))
}

func (p *CronPlugin) reply(message *telebot.Message, text string) {
	if registry.Bot != nil {
		if _, err := registry.Bot.Send(message.Chat, text); err != nil {
			log.Printf("[cron] Failed to respond to command: %v", err)
		}
	}
}

func (p *CronPlugin) replyUsage(message *telebot.Message) {
	p.reply(message, "Usage:\n!cron add <chat_id> \"0 9 * * *\" <alias> <command>\n!cron list [chat_id]\n!cron remove <chat_id> <alias>\n!cron reschedule <chat_id> <alias> \"0 10 * * *\"\n!cron update <chat_id> <alias> <command>\nSchedules use config time_zone: "+configuredLocation().String())
}

func messageSenderIsBotOwner(message *telebot.Message) bool {
	return message != nil && message.Sender != nil && registry.Config.OwnerUsername != "" && strings.EqualFold(message.Sender.Username, registry.Config.OwnerUsername)
}

func parseJob(chatIDText, alias, expression, command string) (cronJob, error) {
	chatID, err := strconv.ParseInt(chatIDText, 10, 64)
	if err != nil {
		return cronJob{}, fmt.Errorf("invalid chat_id")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return cronJob{}, fmt.Errorf("command must not be empty")
	}
	return cronJob{ChatID: chatID, Alias: alias, Expression: expression, Command: command}, nil
}

func loadJob(chatID int64, alias string) (cronJob, error) {
	job := cronJob{ChatID: chatID, Alias: alias}
	err := database.DB.QueryRow(`SELECT expression, command FROM cron_jobs WHERE chat_id = ? AND alias = ?`, chatID, alias).Scan(&job.Expression, &job.Command)
	return job, err
}

func (s *scheduler) loadJobs() error {
	rows, err := database.DB.Query(`SELECT chat_id, alias, expression, command FROM cron_jobs ORDER BY chat_id, alias`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var job cronJob
		if err := rows.Scan(&job.ChatID, &job.Alias, &job.Expression, &job.Command); err != nil {
			return err
		}
		if err := s.addOrReplace(job); err != nil {
			log.Printf("[cron] Skipping invalid job %d/%s: %v", job.ChatID, job.Alias, err)
		}
	}
	return rows.Err()
}

func (s *scheduler) addOrReplace(job cronJob) error {
	schedule, err := parseSchedule(job.Expression, configuredLocation())
	if err != nil {
		return err
	}
	key := jobKey(job.ChatID, job.Alias)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[key]; ok {
		s.cron.Remove(existing)
	}
	s.entries[key] = s.cron.Schedule(schedule, robfigcron.FuncJob(func() {
		dispatchScheduledCommand(job)
	}))
	return nil
}

func (s *scheduler) remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.entries[key]; ok {
		s.cron.Remove(entry)
		delete(s.entries, key)
	}
}

func jobKey(chatID int64, alias string) string {
	return strconv.FormatInt(chatID, 10) + ":" + alias
}

func scheduledCommandText(command string) string {
	command = strings.TrimSpace(command)
	if command == "" || strings.HasPrefix(command, "!") {
		return command
	}

	lower := strings.ToLower(command)
	if strings.HasPrefix(lower, "gooby") || strings.HasPrefix(lower, "губи") || strings.HasPrefix(lower, "губян") {
		return command
	}
	return "Губи, " + command
}

func dispatchScheduledCommand(job cronJob) {
	chat := &telebot.Chat{ID: job.ChatID}
	if database.DB != nil {
		var chatType, title, username, firstName, lastName sql.NullString
		err := database.DB.QueryRow(`SELECT type, title, username, first_name, last_name FROM chats WHERE id = ?`, job.ChatID).Scan(&chatType, &title, &username, &firstName, &lastName)
		if err != nil && err != sql.ErrNoRows {
			log.Printf("[cron] Failed to load target chat %d: %v", job.ChatID, err)
		}
		chat.Type = telebot.ChatType(chatType.String)
		chat.Title, chat.Username, chat.FirstName, chat.LastName = title.String, username.String, firstName.String, lastName.String
	}
	registry.DispatchMessage(&telebot.Message{
		Text:   scheduledCommandText(job.Command),
		Chat:   chat,
		Sender: &telebot.User{Username: registry.Config.OwnerUsername},
	})
}

type locationSchedule struct {
	schedule robfigcron.Schedule
	location *time.Location
}

func (s locationSchedule) Next(t time.Time) time.Time {
	return s.schedule.Next(t.In(s.location))
}

func parseSchedule(expression string, loc *time.Location) (robfigcron.Schedule, error) {
	if loc == nil {
		loc = time.Local
	}
	parser := robfigcron.NewParser(robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow)
	schedule, err := parser.Parse(expression)
	if err != nil {
		return nil, err
	}
	return locationSchedule{schedule: schedule, location: loc}, nil
}
