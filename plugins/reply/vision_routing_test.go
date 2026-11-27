package reply

import (
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	"github.com/tucnak/telebot"
)

type visionRoundTripper func(*http.Request) (*http.Response, error)

func (f visionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVisionPathsHonorChatRouting(t *testing.T) {
	for _, path := range []string{"inspection", "metadata"} {
		for _, tc := range []struct {
			name, global, override, model, key, wantHost, wantModel string
			auth                                                    bool
			wantErr                                                 bool
		}{
			{"chat-orca", "openrouter", "orcarouter", "orcarouter/free", "orca-key", "api.orcarouter.ai", "orcarouter/free", false, false},
			{"orca-key-required", "orcarouter", "", "google/vision", "", "", "", false, true},
			{"chat-codex", "openrouter", "openai-codex", "openai/gpt-6-luna", "", "chatgpt.com", "gpt-6-luna", true, false},
			{"chat-router", "openai-codex", "openrouter", "openai/gpt-6-luna", "test-key", "openrouter.ai", "openai/gpt-6-luna", true, false},
			{"inherited-codex", "openai-codex", "", "openai/gpt-6-luna", "test-key", "chatgpt.com", "gpt-6-luna", true, false},
			{"plain-orca-default", "openai-codex", "", "google/gemini-4-neon:online", "orca-key", "api.orcarouter.ai", "google/gemini-4-neon", true, false},
			{"prefixed-orca", "openai-codex", "", "orcarouter:orcarouter/auto", "orca-key", "api.orcarouter.ai", "orcarouter/auto", true, false},
			{"empty-orca", "openai-codex", "", "orcarouter:", "orca-key", "", "", true, true},
			{"explicit-router", "openai-codex", "", "openrouter:google/vision", "test-key", "openrouter.ai", "google/vision", true, false},
			{"non-openai", "openai-codex", "", "openrouter/google/gemini-4-neon", "test-key", "openrouter.ai", "google/gemini-4-neon", true, false},
			{"fallback", "openai-codex", "", "gpt-6-luna", "test-key", "openrouter.ai", "openai/gpt-6-luna", false, false},
			{"router-key-required", "openrouter", "", "openai/gpt-6-luna", "", "", "", true, true},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				oldDB, oldConfig, oldTransport, oldDownload := database.DB, registry.Config, http.DefaultTransport, downloadTelegramImage
				t.Cleanup(func() {
					database.DB = oldDB
					registry.Config = oldConfig
					http.DefaultTransport = oldTransport
					downloadTelegramImage = oldDownload
				})
				db := testutils.SetupTestDB(t)
				defer db.Close()
				database.DB = db
				if err := registry.EnsurePluginSettingsTable(); err != nil {
					t.Fatal(err)
				}
				registry.Config.AiProvider = tc.global
				registry.Config.AiModel = "google/gemini-4-neon"
				registry.Config.OpenrouterApiKey = tc.key
				registry.Config.OrcarouterApiKey = tc.key
				registry.Config.ImageVisionModel = "google/wrong-global-model"
				chatID := int64(123)
				if tc.override != "" {
					if err := registry.SetPluginSetting(&chatID, registry.ConfigPluginName, registry.AiProviderKey, tc.override); err != nil {
						t.Fatal(err)
					}
				}
				if err := registry.SetPluginSetting(&chatID, registry.ConfigPluginName, registry.ImageVisionModelKey, tc.model); err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				t.Setenv("CODEX_HOME", home)
				if tc.auth {
					if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"local-test-only"}}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				fixture := func() string {
					t.Helper()
					p := filepath.Join(t.TempDir(), "image.jpg")
					f, err := os.Create(p)
					if err != nil {
						t.Fatal(err)
					}
					if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
					return p
				}
				downloadTelegramImage = func(string) (string, error) { return fixture(), nil }
				calls := 0
				http.DefaultTransport = visionRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					if tc.wantHost == "api.orcarouter.ai" && (r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer orca-key") {
						t.Fatalf("wrong Orca path/auth: %s", r.URL)
					}
					if r.URL.Host != tc.wantHost {
						t.Errorf("host=%s want=%s", r.URL.Host, tc.wantHost)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["model"] != tc.wantModel {
						t.Errorf("model=%v want=%s", body["model"], tc.wantModel)
					}
					if tc.wantHost == "api.orcarouter.ai" && strings.HasSuffix(tc.model, ":online") {
						var req struct {
							Tools []struct {
								Type     string `json:"type"`
								Function struct {
									Name string `json:"name"`
								} `json:"function"`
							} `json:"tools"`
						}
						encoded, err := json.Marshal(body)
						if err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(encoded, &req); err != nil {
							t.Fatal(err)
						}
						searchTools := 0
						for _, tool := range req.Tools {
							if tool.Type == "function" && tool.Function.Name == "googleSearch" {
								searchTools++
							}
						}
						if searchTools != 1 {
							t.Fatalf("native googleSearch tools=%d want=1", searchTools)
						}
					}
					raw, _ := json.Marshal(body)
					if !strings.Contains(string(raw), "data:image/jpeg;base64,") {
						t.Errorf("lost image: %s", raw)
					}
					response := `{"choices":[{"message":{"role":"assistant","content":"{\"description\":\"local fixture\"}"}}]}`
					if r.URL.Host == "chatgpt.com" {
						if r.URL.Path != "/backend-api/codex/responses" {
							t.Errorf("path=%s", r.URL.Path)
						}
						if !strings.Contains(string(raw), `"type":"input_image"`) || !strings.Contains(string(raw), `"detail":"low"`) {
							t.Errorf("bad Codex image: %s", raw)
						}
						response = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"local\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"{\\\"description\\\":\\\"local fixture\\\"}\"}]}}\n\ndata: {\"type\":\"response.completed\"}\n\n"
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
				})
				msg := &telebot.Message{Chat: &telebot.Chat{ID: chatID}, Text: "describe fixture"}
				var err error
				if path == "inspection" {
					_, err = analyzeImageWithVision(msg, fixture())
				} else {
					var metadata ImageMetadataDescription
					metadata, err = realDescribeImageForMetadata(msg, &ResolvedImageTarget{FileID: "local"})
					if err == nil && metadata.Description != "local fixture" {
						t.Errorf("metadata=%+v", metadata)
					}
				}
				if (err != nil) != tc.wantErr {
					t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
				}
				wantCalls := 1
				if tc.wantErr {
					wantCalls = 0
				}
				if calls != wantCalls {
					t.Fatalf("calls=%d want=%d", calls, wantCalls)
				}
			})
		}
	}
}
