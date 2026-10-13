package spotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/utils/testutils"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

const testOpus = "anthropic/claude-opus-5.5"
const testGPT = "openai/gpt-6.1-sol"

func ensembleBody(label string, words int) string {
	return label + " " + strings.Repeat("музыка ", words-1)
}
func ensembleResponse(body string, score any) openai.ChatCompletionResponse {
	b, _ := json.Marshal(map[string]any{"review_text": body, "album_rating": score})
	return completionResponse(string(b))
}
func completionResponse(raw string) openai.ChatCompletionResponse {
	return openai.ChatCompletionResponse{ID: "response-id", Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: raw}}}}
}
func ensembleTestDeps(t *testing.T) (spotifyReviewDependencies, *spotifyReviewAudit) {
	t.Helper()
	old := registry.Config
	t.Cleanup(func() { registry.Config = old })
	registry.Config = registry.Configuration{SpotifyReviewPrompt: "Installed voice {artist} {title} {grounding}"}
	audit := new(spotifyReviewAudit)
	return spotifyReviewDependencies{
		research: func(context.Context, string, string, string, string, *SpotifyAlbum) spotifyResearch {
			return spotifyResearch{Sources: []spotifyCriticism{{Publication: "Editorial", Praise: "SUPPORTED MUSIC"}}}
		},
		shuffle: func(order []int) {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		},
		audit: func(a spotifyReviewAudit) error { *audit = a; return nil },
	}, audit
}
func ensembleTestOptions() spotifyReviewEnsembleOptions {
	return spotifyReviewEnsembleOptions{Models: []string{testOpus, testGPT}, Timeout: time.Second}
}
func runEnsemble(ctx context.Context, opts spotifyReviewEnsembleOptions, deps spotifyReviewDependencies) (string, sql.NullFloat64, error) {
	review, rating, _, err := generateSpotifyReviewWithDeps(ctx, nil, "album", "Artist", "Title", "2026", nil, opts, deps)
	return review, rating, err
}

func TestSpotifyEnsembleSharedResearchParallelBlindExactSelection(t *testing.T) {
	deps, audit := ensembleTestDeps(t)
	researches := 0
	originalResearch := deps.research
	deps.research = func(ctx context.Context, typ, artist, title, year string, album *SpotifyAlbum) spotifyResearch {
		researches++
		return originalResearch(ctx, typ, artist, title, year, album)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	prompts := map[string]string{}
	opusBody, gptBody := ensembleBody("OPUS-TEXT", 150), ensembleBody("GPT-TEXT", 200)
	deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
			mu.Lock()
			defer mu.Unlock()
			if len(prompts) != 2 || prompts[testOpus] != prompts[testGPT] {
				t.Errorf("different writer prompts: %v", prompts)
			}
			if !strings.Contains(prompts[testOpus], "Installed voice") || !strings.Contains(prompts[testOpus], "Do not manufacture quotes") || !strings.Contains(prompts[testOpus], "150") {
				t.Error("voice, safety or length contract lost")
			}
			if model != testGPT {
				t.Errorf("default judge = %s", model)
			}
			payload := req.Messages[len(req.Messages)-1].Content
			for _, leak := range []string{testOpus, testGPT, "response-id"} {
				if strings.Contains(payload, leak) {
					t.Errorf("judge identity leak: %s", leak)
				}
			}
			var input struct {
				Candidates []struct {
					ID          string   `json:"id"`
					ReviewText  string   `json:"review_text"`
					AlbumRating *float64 `json:"album_rating"`
				}
			}
			if err := json.Unmarshal([]byte(payload), &input); err != nil {
				t.Error(err)
			}
			if len(input.Candidates) != 2 || input.Candidates[0].ReviewText != strings.TrimSpace(gptBody) || input.Candidates[1].ReviewText != strings.TrimSpace(opusBody) {
				t.Errorf("shuffle mapping: %+v", input)
			}
			if !strings.Contains(payload, "SUPPORTED MUSIC") {
				t.Error("judge lost shared grounding")
			}
			return completionResponse(`{"selected_id":"candidate_1","reason":"better musical judgment","rejected_ids":["candidate_2"]}`), nil
		}
		mu.Lock()
		prompts[model] = req.Messages[0].Content
		mu.Unlock()
		started <- model
		select {
		case <-release:
		case <-ctx.Done():
			return openai.ChatCompletionResponse{}, ctx.Err()
		}
		if model == testOpus {
			return ensembleResponse(opusBody, 9.125), nil
		}
		return ensembleResponse(gptBody, 3.75), nil
	}
	go func() { <-started; <-started; close(release) }()
	review, rating, err := runEnsemble(context.Background(), ensembleTestOptions(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if researches != 1 {
		t.Fatalf("research calls = %d", researches)
	}
	if review != strings.TrimSpace(gptBody)+"\n\n3,75 / 10" || !rating.Valid || rating.Float64 != 3.75 {
		t.Fatalf("not exact winning text/score: %q %+v", review, rating)
	}
	if audit.Decision != "judge_selected" || audit.SelectedModel != testGPT || len(audit.Candidates) != 2 || audit.Candidates[0].ResponseID != "response-id" || audit.Candidates[0].Raw == "" {
		t.Fatalf("audit: %+v", audit)
	}
}

func TestSpotifyEnsembleFailurePolicy(t *testing.T) {
	cases := []struct {
		name, opus, gpt, judge string
		wantModel, decision    string
		wantErr                bool
	}{
		{"judge provider failure", "valid", "valid", "provider", testOpus, "judge_failed_fallback", false},
		{"judge invalid ID", "valid", "valid", `{"selected_id":"invented","reason":"x","rejected_ids":[]}`, testOpus, "judge_failed_fallback", false},
		{"judge invalid JSON", "valid", "valid", `garbage`, testOpus, "judge_failed_fallback", false},
		{"judge incomplete decision", "valid", "valid", `{"selected_id":null}`, testOpus, "judge_failed_fallback", false},
		{"explicit rejection", "valid", "valid", `{"selected_id":null,"reason":"both invent facts","rejected_ids":["candidate_1","candidate_2"]}`, "", "judge_rejected_all", true},
		{"all invalid", "short", "provider", "provider", "", "no_valid_candidates", true},
		{"opus invalid fallback other", "short", "valid", "provider", testGPT, "judge_failed_fallback", false},
		{"gpt provider failure", "valid", "provider", `{"selected_id":"candidate_1","reason":"sound","rejected_ids":[]}`, testOpus, "judge_selected", false},
		{"opus candidate timeout", "timeout", "valid", "provider", testGPT, "judge_failed_fallback", false},
		{"judge timeout", "valid", "valid", "timeout", testOpus, "judge_failed_fallback", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, audit := ensembleTestDeps(t)
			opts := ensembleTestOptions()
			opts.Timeout = 30 * time.Millisecond
			deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
				mode := tc.gpt
				if model == testOpus {
					mode = tc.opus
				}
				if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
					mode = tc.judge
				}
				switch mode {
				case "provider":
					return openai.ChatCompletionResponse{}, errors.New("provider error containing secret should not be logged")
				case "timeout":
					<-ctx.Done()
					return openai.ChatCompletionResponse{}, ctx.Err()
				case "short":
					return ensembleResponse("short", 7), nil
				case "valid":
					return ensembleResponse(ensembleBody(model, 160), 7), nil
				default:
					return completionResponse(mode), nil
				}
			}
			review, _, err := runEnsemble(context.Background(), opts, deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if tc.wantErr && review != "" {
				t.Error("rejected review returned for publication")
			}
			if audit.Decision != tc.decision || audit.SelectedModel != tc.wantModel {
				t.Fatalf("audit decision=%s model=%s", audit.Decision, audit.SelectedModel)
			}
			raw, _ := json.Marshal(audit)
			if strings.Contains(string(raw), "containing secret") {
				t.Error("audit leaked upstream error")
			}
		})
	}
}

func TestSpotifyEnsembleParentCancellationNeverFallsBack(t *testing.T) {
	for _, phase := range []string{"research", "candidates", "judge"} {
		t.Run(phase, func(t *testing.T) {
			deps, audit := ensembleTestDeps(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "research" {
				deps.research = func(context.Context, string, string, string, string, *SpotifyAlbum) spotifyResearch {
					cancel()
					return spotifyResearch{}
				}
			}
			deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
				if phase == "candidates" || (phase == "judge" && req.ResponseFormat.JSONSchema.Name == "spotify_review_judge") {
					cancel()
					<-ctx.Done()
					return openai.ChatCompletionResponse{}, ctx.Err()
				}
				return ensembleResponse(ensembleBody("valid", 160), 8), nil
			}
			review, _, err := runEnsemble(ctx, ensembleTestOptions(), deps)
			if !errors.Is(err, context.Canceled) || review != "" {
				t.Fatalf("cancellation published/fell back: %q %v", review, err)
			}
			if phase != "research" && audit.Decision != "canceled" {
				t.Fatalf("decision=%s", audit.Decision)
			}
		})
	}
}

func TestSpotifyEnsembleParentDeadlineUncooperativeProvider(t *testing.T) {
	for _, phase := range []string{"candidates", "judge"} {
		t.Run(phase, func(t *testing.T) {
			deps, audit := ensembleTestDeps(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
				isJudge := req.ResponseFormat.JSONSchema.Name == "spotify_review_judge"
				if (phase == "candidates" && model == testGPT) || (phase == "judge" && isJudge) {
					<-release
				}
				if isJudge {
					return completionResponse(`{"selected_id":"candidate_1","reason":"fine","rejected_ids":[]}`), nil
				}
				return ensembleResponse(ensembleBody("valid", 170), 9), nil
			}
			review, _, err := runEnsemble(ctx, ensembleTestOptions(), deps)
			if !errors.Is(err, context.DeadlineExceeded) || review != "" || audit.Decision != "canceled" {
				t.Fatalf("deadline fell back: %q %v %+v", review, err, audit)
			}
		})
	}
}

func TestSpotifyEnsembleLengthAndTrackNull(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		words     int
		score     any
		valid     bool
	}{
		{"149", "album", 149, 8, false}, {"150", "album", 150, 8, true}, {"200", "album", 200, 8, true}, {"201", "album", 201, 8, false},
		{"track null", "track", 170, nil, true}, {"track numeric", "track", 170, 8, false}, {"album null", "album", 170, nil, false}, {"overflow", "album", 170, json.Number("1e9999"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := ensembleTestDeps(t)
			opts := ensembleTestOptions()
			opts.Models = []string{testOpus}
			deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
				if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
					return completionResponse(`{"selected_id":"candidate_1","reason":"coherent","rejected_ids":[]}`), nil
				}
				return ensembleResponse(ensembleBody("word—word", tc.words), tc.score), nil
			}
			review, rating, _, err := generateSpotifyReviewWithDeps(context.Background(), nil, tc.typ, "Artist", "Title", "2026", nil, opts, deps)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && tc.typ == "track" && (rating.Valid || strings.Contains(review, "/ 10")) {
				t.Fatal("track score leaked")
			}
		})
	}
}

func TestSpotifyEnsembleOptOutUsesLegacy(t *testing.T) {
	deps, _ := ensembleTestDeps(t)
	legacyCalls := 0
	deps.legacy = func(ctx context.Context, chatID *int64, prompt, typ string) (string, sql.NullFloat64, string, error) {
		legacyCalls++
		if strings.Contains(prompt, "150–200") {
			t.Error("ensemble constraints leaked into legacy")
		}
		return "legacy", sql.NullFloat64{}, testGPT, nil
	}
	deps.complete = func(context.Context, string, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		t.Error("ensemble used on opt-out")
		return openai.ChatCompletionResponse{}, nil
	}
	deps.audit = func(spotifyReviewAudit) error { t.Error("ensemble audit used on opt-out"); return nil }
	review, _, err := runEnsemble(context.Background(), spotifyReviewEnsembleOptions{}, deps)
	if err != nil || review != "legacy" || legacyCalls != 1 {
		t.Fatalf("legacy=%q calls=%d err=%v", review, legacyCalls, err)
	}
}

func TestSpotifyEnsembleAuditFailureBlocksPublication(t *testing.T) {
	deps, _ := ensembleTestDeps(t)
	deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
			return completionResponse(`{"selected_id":"candidate_1","reason":"fine","rejected_ids":[]}`), nil
		}
		return ensembleResponse(ensembleBody("valid", 160), 6), nil
	}
	deps.audit = func(spotifyReviewAudit) error { return errors.New("disk full") }
	review, _, err := runEnsemble(context.Background(), ensembleTestOptions(), deps)
	if err == nil || review != "" {
		t.Fatalf("audit failure publishes: %q %v", review, err)
	}
}

func TestSpotifyEnsembleBareGPTProvenance(t *testing.T) {
	deps, _ := ensembleTestDeps(t)
	opts := ensembleTestOptions()
	opts.Models = []string{"gpt-6.1-sol"}
	deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
			return completionResponse(`{"selected_id":"candidate_1","reason":"fine","rejected_ids":[]}`), nil
		}
		return ensembleResponse(ensembleBody("valid", 170), 7), nil
	}
	_, _, model, err := generateSpotifyReviewWithDeps(context.Background(), nil, "album", "Artist", "Title", "2026", nil, opts, deps)
	if err != nil || model != testGPT {
		t.Fatalf("full ID=%q err=%v", model, err)
	}
}

func TestSpotifyEnsembleSelectedModelStorageNotJudge(t *testing.T) {
	deps, audit := ensembleTestDeps(t)
	db := testutils.SetupTestDB(t)
	oldDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = oldDB; db.Close() })
	createSpotifyReviewsTable(t, db)
	deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
			return completionResponse(`{"selected_id":"candidate_2","reason":"independent and grounded","rejected_ids":[]}`), nil
		}
		resp := ensembleResponse(ensembleBody("candidate", 170), 8.125)
		resp.Model = "gpt-short-alias"
		return resp, nil
	}
	review, rating, model, err := generateSpotifyReviewWithDeps(context.Background(), nil, "album", "Artist", "Title", "2026", nil, ensembleTestOptions(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveGeneratedReviewText("album", "selected", review, rating, model); err != nil {
		t.Fatal(err)
	}
	var storedModel, text string
	var score float64
	if err := db.QueryRow(`SELECT generated_by_model,review_text,album_rating FROM spotify_reviews WHERE item_key='selected'`).Scan(&storedModel, &text, &score); err != nil {
		t.Fatal(err)
	}
	if storedModel != testOpus || storedModel == audit.JudgeModel || text != review || score != 8.125 {
		t.Fatalf("wrong provenance: stored=%s judge=%s score=%v", storedModel, audit.JudgeModel, score)
	}
}

type spotifyTestTransport func(*http.Request) (*http.Response, error)

func (f spotifyTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSpotifyEnsembleProductionRouterAndLegacyModelProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, provider, configured, want string
		legacy                           bool
	}{
		{"ensemble router", "openrouter", testGPT, testGPT, false},
		{"ensemble codex provider nonGPT routes router", "openai-codex", testOpus, testOpus, false},
		{"legacy full token", "openrouter", testGPT, testGPT, true},
		{"legacy bare GPT maps provider ID", "openrouter", "gpt-6.1-sol", testGPT, true},
		{"legacy default model", "openai", "", "openai/gpt-4o-mini", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldConfig, oldTransport, oldDB := registry.Config, http.DefaultTransport, database.DB
			t.Cleanup(func() { registry.Config = oldConfig; http.DefaultTransport = oldTransport; database.DB = oldDB })
			db := testutils.SetupTestDB(t)
			t.Cleanup(func() { db.Close() })
			database.DB = db
			if err := registry.EnsurePluginSettingsTable(); err != nil {
				t.Fatal(err)
			}
			createSpotifyReviewsTable(t, db)
			registry.Config = registry.Configuration{AiProvider: tc.provider, AiModel: tc.configured, OpenrouterApiKey: "test-key", OpenaiApiKey: "test-key"}
			http.DefaultTransport = spotifyTestTransport(func(req *http.Request) (*http.Response, error) {
				var payload struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				expected := tc.configured
				if tc.legacy && tc.configured == "" {
					expected = "gpt-4o-mini"
				}
				if payload.Model != expected {
					t.Fatalf("routed model=%q want=%q", payload.Model, expected)
				}
				if tc.provider != "openai" && req.URL.Host != "openrouter.ai" {
					t.Fatalf("wrong route=%s", req.URL.Host)
				}
				resp := ensembleResponse(ensembleBody("body", 170), 7.25)
				resp.Model = "short-response-alias"
				data, _ := json.Marshal(resp)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
			})
			if tc.legacy {
				review, rating, model, err := callChatModelForReviewContext(context.Background(), nil, "legacy prompt", "album")
				if err != nil {
					t.Fatal(err)
				}
				if model != tc.want {
					t.Fatalf("legacy generatedBy=%q want=%q", model, tc.want)
				}
				if err := saveGeneratedReviewText("album", "legacy", review, rating, model); err != nil {
					t.Fatal(err)
				}
				var stored string
				if err := db.QueryRow(`SELECT generated_by_model FROM spotify_reviews WHERE item_key='legacy'`).Scan(&stored); err != nil || stored != tc.want {
					t.Fatalf("stored=%s err=%v", stored, err)
				}
			} else {
				_, err := completeSpotifyReviewModel(context.Background(), nil, tc.configured, buildSpotifyReviewCompletionRequest(tc.configured, "shared prompt", "album"))
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSpotifyEnsembleAuditAppend(t *testing.T) {
	dir := t.TempDir()
	a := spotifyReviewAudit{Decision: "judge_selected", SelectedModel: testOpus, Candidates: []spotifyReviewCandidate{{Model: testOpus, Raw: "raw structured response", ResponseID: "id", Rating: sql.NullFloat64{Float64: 7.2, Valid: true}}}}
	for i := 0; i < 2; i++ {
		if err := appendSpotifyReviewAudit(dir, a); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "spotify-review-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("append lines=%d", len(lines))
	}
	for _, line := range lines {
		var got spotifyReviewAudit
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatal(err)
		}
		if got.Candidates[0].ResponseID != "id" || got.SelectedModel != testOpus {
			t.Fatalf("audit readback=%+v", got)
		}
	}
	info, _ := os.Stat(filepath.Join(dir, "spotify-review-audit.jsonl"))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("audit permissions=%v", info.Mode())
	}
}
