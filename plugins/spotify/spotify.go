package spotify

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

const (
	SpotifyPluginName       = "spotify"
	SpotifyEnabledKey       = "enabled"
	SpotifyReviewEnabledKey = "review_enabled"
	SpotifyAuthURL          = "https://accounts.spotify.com/api/token"
	SpotifyAPIBaseURL       = "https://api.spotify.com/v1"
)

type SpotifyPlugin struct {
	albumRegex  *regexp.Regexp
	trackRegex  *regexp.Regexp
	accessToken string
	tokenExpiry time.Time
}

type SpotifyAlbum struct {
	Name        string `json:"name"`
	ReleaseDate string `json:"release_date"`
	Images      []struct {
		URL    string `json:"url"`
		Height int    `json:"height"`
		Width  int    `json:"width"`
	} `json:"images"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
}

type SpotifyTrack struct {
	Name  string `json:"name"`
	Album struct {
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
		Images      []struct {
			URL    string `json:"url"`
			Height int    `json:"height"`
			Width  int    `json:"width"`
		} `json:"images"`
	} `json:"album"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
}

func init() {
	registry.RegisterPlugin(&SpotifyPlugin{})
}

func (p *SpotifyPlugin) Start(_ interface{}) {
	p.albumRegex = regexp.MustCompile(`https://open\.spotify\.com/album/([a-zA-Z0-9]+)`)
	p.trackRegex = regexp.MustCompile(`https://open\.spotify\.com/track/([a-zA-Z0-9]+)`)
	log.Println("[spotify] Plugin started")
}

func (p *SpotifyPlugin) Process(message *telebot.Message) {
	// Check if Spotify configuration exists
	if registry.Config.SpotifyConfig.ClientID == "" || registry.Config.SpotifyConfig.ClientSecret == "" {
		return
	}

	// Check if plugin is enabled globally
	if !p.isEnabled(nil) {
		return
	}

	// Check if plugin is enabled for this chat
	chatID := message.Chat.ID
	if !p.isEnabled(&chatID) {
		return
	}

	// Find Spotify album links in the message
	albumMatches := p.albumRegex.FindAllStringSubmatch(message.Text, -1)
	for _, match := range albumMatches {
		if len(match) > 1 {
			albumID := match[1]
			p.processAlbum(message, albumID)
		}
	}

	// Find Spotify track links in the message
	trackMatches := p.trackRegex.FindAllStringSubmatch(message.Text, -1)
	for _, match := range trackMatches {
		if len(match) > 1 {
			trackID := match[1]
			p.processTrack(message, trackID)
		}
	}
}

func (p *SpotifyPlugin) isEnabled(chatID *int64) bool {
	enabled := registry.GetPluginSetting(chatID, SpotifyPluginName, SpotifyEnabledKey, "true")
	return enabled == "true"
}

func (p *SpotifyPlugin) isReviewEnabled(chatID *int64) bool {
	enabled := registry.GetPluginSetting(chatID, SpotifyPluginName, SpotifyReviewEnabledKey, "true")
	return enabled == "true"
}

func (p *SpotifyPlugin) processAlbum(message *telebot.Message, albumID string) {
	// Ensure we have a valid access token
	if err := p.ensureAccessToken(); err != nil {
		log.Printf("[spotify] Failed to get access token: %v", err)
		return
	}

	// Fetch album data from Spotify API
	album, err := p.fetchAlbum(albumID)
	if err != nil {
		log.Printf("[spotify] Failed to fetch album %s: %v", albumID, err)
		return
	}

	// Get the highest quality image
	var imageURL string
	maxSize := 0
	for _, img := range album.Images {
		size := img.Height * img.Width
		if size > maxSize {
			maxSize = size
			imageURL = img.URL
		}
	}

	if imageURL == "" {
		log.Printf("[spotify] No album art found for album %s", albumID)
		return
	}

	// Download the image
	imageData, err := p.downloadImage(imageURL)
	if err != nil {
		log.Printf("[spotify] Failed to download album art: %v", err)
		return
	}

	// Extract year from release date
	year := ""
	if len(album.ReleaseDate) >= 4 {
		year = album.ReleaseDate[:4]
	}

	// Build artist names
	var artists []string
	for _, artist := range album.Artists {
		artists = append(artists, artist.Name)
	}
	artistName := strings.Join(artists, ", ")

	// Generate fancy preview image
	previewData, err := generateSpotifyPreview(imageData, album.Name, artistName, year)
	if err != nil {
		log.Printf("[spotify] Failed to generate preview image: %v", err)
		// Fall back to original image if preview generation fails
		previewData = imageData
	}

	// Build caption with links
	albumURL := fmt.Sprintf("https://open.spotify.com/album/%s", albumID)
	searchQuery := url.QueryEscape(fmt.Sprintf("%s %s %s", artistName, album.Name, year))
	ddgURL := fmt.Sprintf("https://duckduckgo.com/?q=%s", searchQuery)
	caption := fmt.Sprintf("[Spotify](%s) | [DDG](%s)", albumURL, ddgURL)
	// Optionally generate and publish a funny review
	if p.isReviewEnabled(&message.Chat.ID) {
		if reviewURL := generateAndPublishReview(message.Chat.ID, "album", albumID, artistName, album.Name, year); reviewURL != "" {
			caption = fmt.Sprintf("%s | [Рецензия от Губи](%s)", caption, reviewURL)
		}
	}

	// Save image to a temporary file
	tempFile, err := ioutil.TempFile("", "spotify-album-*.jpg")
	if err != nil {
		log.Printf("[spotify] Failed to create temp file: %v", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := tempFile.Write(previewData); err != nil {
		log.Printf("[spotify] Failed to write image to temp file: %v", err)
		return
	}

	// Send the album art with caption and Markdown parsing as a reply
	photo := &telebot.Photo{
		File:    telebot.FromDisk(tempFile.Name()),
		Caption: caption,
	}

	bot := registry.Bot
	if _, err := bot.Send(message.Chat, photo, &telebot.SendOptions{
		ParseMode: telebot.ModeMarkdown,
		ReplyTo:   message,
	}); err != nil {
		log.Printf("[spotify] Failed to send album art: %v", err)
	}
}

func (p *SpotifyPlugin) processTrack(message *telebot.Message, trackID string) {
	// Ensure we have a valid access token
	if err := p.ensureAccessToken(); err != nil {
		log.Printf("[spotify] Failed to get access token: %v", err)
		return
	}

	// Fetch track data from Spotify API
	track, err := p.fetchTrack(trackID)
	if err != nil {
		log.Printf("[spotify] Failed to fetch track %s: %v", trackID, err)
		return
	}

	// Get the highest quality image from the album
	var imageURL string
	maxSize := 0
	for _, img := range track.Album.Images {
		size := img.Height * img.Width
		if size > maxSize {
			maxSize = size
			imageURL = img.URL
		}
	}

	if imageURL == "" {
		log.Printf("[spotify] No album art found for track %s", trackID)
		return
	}

	// Download the image
	imageData, err := p.downloadImage(imageURL)
	if err != nil {
		log.Printf("[spotify] Failed to download album art: %v", err)
		return
	}

	// Extract year from release date
	year := ""
	if len(track.Album.ReleaseDate) >= 4 {
		year = track.Album.ReleaseDate[:4]
	}

	// Build artist names
	var artists []string
	for _, artist := range track.Artists {
		artists = append(artists, artist.Name)
	}
	artistName := strings.Join(artists, ", ")

	// Generate fancy preview image
	previewData, err := generateSpotifyPreview(imageData, track.Name, artistName, year)
	if err != nil {
		log.Printf("[spotify] Failed to generate preview image: %v", err)
		// Fall back to original image if preview generation fails
		previewData = imageData
	}

	trackURL := fmt.Sprintf("https://open.spotify.com/track/%s", trackID)
	searchQuery := url.QueryEscape(fmt.Sprintf("%s %s %s", artistName, track.Name, year))
	ddgURL := fmt.Sprintf("https://duckduckgo.com/?q=%s", searchQuery)
	caption := fmt.Sprintf("[Spotify](%s) | [DDG](%s)", trackURL, ddgURL)

	// Save image to a temporary file
	tempFile, err := ioutil.TempFile("", "spotify-track-*.jpg")
	if err != nil {
		log.Printf("[spotify] Failed to create temp file: %v", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := tempFile.Write(previewData); err != nil {
		log.Printf("[spotify] Failed to write image to temp file: %v", err)
		return
	}

	// Send the track art with caption and Markdown parsing as a reply
	photo := &telebot.Photo{
		File:    telebot.FromDisk(tempFile.Name()),
		Caption: caption,
	}

	bot := registry.Bot
	if _, err := bot.Send(message.Chat, photo, &telebot.SendOptions{
		ParseMode: telebot.ModeMarkdown,
		ReplyTo:   message,
	}); err != nil {
		log.Printf("[spotify] Failed to send track art: %v", err)
	}
}

func (p *SpotifyPlugin) ensureAccessToken() error {
	// Check if token is still valid
	if time.Now().Before(p.tokenExpiry) && p.accessToken != "" {
		return nil
	}

	// Get new access token
	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequest("POST", SpotifyAuthURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}

	// Set headers
	auth := base64.StdEncoding.EncodeToString([]byte(
		registry.Config.SpotifyConfig.ClientID + ":" + registry.Config.SpotifyConfig.ClientSecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Make request
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed: %s - %s", resp.Status, string(body))
	}

	// Parse response
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return err
	}

	p.accessToken = tokenResp.AccessToken
	p.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)

	return nil
}

func (p *SpotifyPlugin) fetchAlbum(albumID string) (*SpotifyAlbum, error) {
	url := fmt.Sprintf("%s/albums/%s", SpotifyAPIBaseURL, albumID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed: %s - %s", resp.Status, string(body))
	}

	var album SpotifyAlbum
	if err := json.NewDecoder(resp.Body).Decode(&album); err != nil {
		return nil, err
	}

	return &album, nil
}

func (p *SpotifyPlugin) fetchTrack(trackID string) (*SpotifyTrack, error) {
	url := fmt.Sprintf("%s/tracks/%s", SpotifyAPIBaseURL, trackID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+p.accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed: %s - %s", resp.Status, string(body))
	}

	var track SpotifyTrack
	if err := json.NewDecoder(resp.Body).Decode(&track); err != nil {
		return nil, err
	}

	return &track, nil
}

func (p *SpotifyPlugin) downloadImage(url string) ([]byte, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download image: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// EnableForChat enables the Spotify plugin for a specific chat
func EnableForChat(chatID int64) error {
	return registry.SetPluginSetting(&chatID, SpotifyPluginName, SpotifyEnabledKey, "true")
}

// DisableForChat disables the Spotify plugin for a specific chat
func DisableForChat(chatID int64) error {
	return registry.SetPluginSetting(&chatID, SpotifyPluginName, SpotifyEnabledKey, "false")
}

// EnableGlobally enables the Spotify plugin globally
func EnableGlobally() error {
	return registry.SetPluginSetting(nil, SpotifyPluginName, SpotifyEnabledKey, "true")
}

// DisableGlobally disables the Spotify plugin globally
func DisableGlobally() error {
	return registry.SetPluginSetting(nil, SpotifyPluginName, SpotifyEnabledKey, "false")
}

// EnableReviewsForChat enables review generation for a specific chat
func EnableReviewsForChat(chatID int64) error {
	return registry.SetPluginSetting(&chatID, SpotifyPluginName, SpotifyReviewEnabledKey, "true")
}

// DisableReviewsForChat disables review generation for a specific chat
func DisableReviewsForChat(chatID int64) error {
	return registry.SetPluginSetting(&chatID, SpotifyPluginName, SpotifyReviewEnabledKey, "false")
}

// EnableReviewsGlobally enables review generation globally
func EnableReviewsGlobally() error {
	return registry.SetPluginSetting(nil, SpotifyPluginName, SpotifyReviewEnabledKey, "true")
}

// DisableReviewsGlobally disables review generation globally
func DisableReviewsGlobally() error {
	return registry.SetPluginSetting(nil, SpotifyPluginName, SpotifyReviewEnabledKey, "false")
}
