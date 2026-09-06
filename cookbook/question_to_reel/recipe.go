// Package question_to_reel turns an approved answer into a captioned B-roll reply.
package question_to_reel

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
)

func init() { cookbook.Register(&Recipe{}) }

type Recipe struct{}

const defaultQuestion = "Why don't clouds fall?"
const defaultAnswer = "Clouds contain tiny water droplets. Those droplets fall so slowly that rising air can keep them suspended. When droplets grow large enough, they can fall through that rising air as rain. A cloud isn't weightless; its droplets are just small."
const defaultBeats = `Use these fractions of total duration:
0–12%: show the question immediately on two balanced lines.
12–24%: TINY WATER DROPS heading and six cyan dots in a loose cluster, gently drifting down.
24–51%: RISING AIR / HOLDS THEM UP. Yellow arrows repeatedly rise on either side of the tiny dots; the dots stay nearly suspended.
51–80%: BIGGER DROPS / FALL AS RAIN. Three waves of three larger blue drops fall; remove the upward arrows.
80–100%: NOT WEIGHTLESS. / JUST TINY. Compare small suspended dots with an upward arrow on the left and two larger falling drops on the right.
These are illustrative diagrams, not measured sizes or a claim that clouds contain no ice.`

func (*Recipe) Manifest() cookbook.Manifest {
	return cookbook.Manifest{
		Slug: "question-to-reel", Title: "Audience question → animated visual answer",
		Description:    "Turn your approved answer and visual beats into a narrated vertical explainer with stock footage, timed diagrams, and synchronized captions.",
		IntroVoiceover: "Show the answer, not just the words. Supply the facts and visual beats; narration, stock footage, moving diagrams and captions turn them into an explainer you can reuse across your channels.",
		Category:       "tutorial", Tags: []string{"questions", "explainer", "b-roll", "captions", "vertical-video"}, Audience: []string{"creator", "educator", "social team"},
		PublishedAt: "2026-09-06", UpdatedAt: "2026-09-06", SeeAlso: []string{"clip-factory"},
		Inputs: []cookbook.Input{
			{Name: "question", Type: cookbook.String, Default: defaultQuestion, Description: "Audience question, read aloud and shown on screen. Maximum 80 characters."},
			{Name: "answer", Type: cookbook.String, Default: defaultAnswer, Description: "Your fact-checked answer. Keep question plus answer between 20 and 60 words."},
			{Name: "footage_query", Type: cookbook.String, Default: "vertical clouds sky", Description: "Concrete portrait stock-footage subject matching your answer."},
			{Name: "visual_beats", Type: cookbook.String, Default: defaultBeats, Description: "Approved labels and simple diagrams, with timing as percentages of narration duration. Supply matching beats when changing the question or answer."},
		},
		Chain: []cookbook.ChainStep{
			{Pipeline: "audio-generator", ArtifactKind: cookbook.Audio, WhatItDoes: "Reads the question and approved answer with MiniMax Speech 2.8 Turbo.", With: map[string]any{"model": "minimax-speech-2-8-turbo"}},
			{Pipeline: "transcription", ArtifactKind: cookbook.Text, WhatItDoes: "Measures the narration and creates the transcript used for caption timing."},
			{Pipeline: "footage-search", ArtifactKind: cookbook.NoneKind, WhatItDoes: "Finds stock footage; selects two distinct clips long enough to cover the narration and records their sources."},
			{Pipeline: "video-reel", ArtifactKind: cookbook.Video, WhatItDoes: "Assembles the two clips in vertical framing with narration, using the measured audio length."},
			{Pipeline: "text-card", ArtifactKind: cookbook.Video, WhatItDoes: "Animates the approved explanatory labels, arrows, and shapes over the footage."},
			{Pipeline: "captions", ArtifactKind: cookbook.Video, WhatItDoes: "Adds bold yellow phrase captions below the diagrams, synchronized to the answer."},
		},
		ExampleCommand: "pipe2 recipe run question-to-reel",
		AgentPrompt:    "Run the question-to-reel recipe with the pipe2 CLI. Supply a question, a fact-checked answer, matching visual beats and a footage query. Review the diagrams, selected footage and final captions, then report the final video URL and footage attribution.",
	}
}

func (*Recipe) Run(ctx *cookbook.Context) error {
	beats := strings.TrimSpace(ctx.Inputs.String("visual_beats"))
	if beats == "" || utf8.RuneCountInString(beats) > 4000 {
		return fmt.Errorf("visual beats must contain 1–4000 characters")
	}
	if (ctx.Inputs.String("question") != defaultQuestion || ctx.Inputs.String("answer") != defaultAnswer) && beats == defaultBeats {
		return fmt.Errorf("supply matching --visual-beats when changing the question or answer")
	}
	if question := strings.TrimSpace(ctx.Inputs.String("question")); question == "" || utf8.RuneCountInString(question) > 80 {
		return fmt.Errorf("question must contain 1–80 characters for the on-screen hook")
	}
	text := strings.TrimSpace(ctx.Inputs.String("question")) + " " + strings.TrimSpace(ctx.Inputs.String("answer"))
	words := len(strings.Fields(text))
	if words < 20 || words > 60 || strings.TrimSpace(ctx.Inputs.String("footage_query")) == "" {
		return fmt.Errorf("supply a footage query and 20–60 words across the question and answer")
	}
	speech, err := ctx.RunPipeline("audio-generator", cookbook.Inputs{"text": text, "model": "minimax-speech-2-8-turbo", "voice": "English_expressive_narrator", "enhance_prompt": false, "instructions": "Read conversationally and clearly. Say only the supplied words; no introduction or added commentary."})
	if err != nil {
		return err
	}
	if err = ctx.Capture(1, speech.URL("audio_url")); err != nil {
		return err
	}
	transcript, err := ctx.RunPipeline("transcription", cookbook.Inputs{"source_asset_id": speech.URL("audio_url"), "language_code": "en", "diarize": false})
	if err != nil {
		return err
	}
	duration := transcript.Float("duration_sec", 0)
	dry := strings.HasPrefix(transcript.RunID, "dry-")
	if dry {
		duration = 18
	}
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 5 || duration > 30 {
		return fmt.Errorf("narration duration %.2f is outside 5–30 seconds", duration)
	}
	if err = ctx.Capture(2, transcript.URL("txt_asset_url")); err != nil {
		return err
	}
	footage, err := ctx.RunPipeline("footage-search", cookbook.Inputs{"query": ctx.Inputs.String("footage_query"), "max_per_source": 20})
	if err != nil {
		return err
	}
	var candidates []struct {
		Title       string  `json:"title"`
		URL         string  `json:"url"`
		Duration    float64 `json:"duration"`
		Width       int     `json:"width"`
		Height      int     `json:"height"`
		Source      string  `json:"source"`
		License     string  `json:"license"`
		Attribution string  `json:"attribution"`
		PageURL     string  `json:"page_url"`
	}
	raw, err := json.Marshal(footage.Output["results"])
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &candidates); err != nil {
		return fmt.Errorf("decode footage: %w", err)
	}
	clips := []string{}
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c.Title), "ai generated") {
			continue
		}
		if c.Duration < duration/2+1 || c.Width < 720 || c.Height < 1280 || math.Abs(float64(c.Width)/float64(c.Height)/(9.0/16)-1) > 0.02 || !strings.HasPrefix(c.URL, "https://") || c.License == "" {
			continue
		}
		if len(clips) > 0 && clips[0] == c.URL {
			continue
		}
		clips = append(clips, c.URL)
		ctx.Logf("Footage: %s | %s | %s | %s", c.PageURL, c.Source, c.Attribution, c.License)
		if len(clips) == 2 {
			break
		}
	}
	if dry {
		clips = []string{"dry://footage-1", "dry://footage-2"}
	}
	if len(clips) != 2 {
		return fmt.Errorf("need two native 9:16 HD stock clips each at least %.1f seconds; refine --footage-query", duration/2+1)
	}
	reel, err := ctx.RunPipeline("video-reel", cookbook.Inputs{"video_urls": clips, "narration_url": speech.URL("audio_url"), "target_aspect": "9:16", "target_duration_sec": duration, "preserve_source_audio": false, "instructions": "Hard cut only, transition duration zero. Speed 1.0 for both clips. Preserve original colors. No intro or outro fades. Narration must play completely from time zero; no music."})
	if err != nil {
		return err
	}
	if err = ctx.Capture(4, reel.URL("video_url")); err != nil {
		return err
	}
	motionInstructions := fmt.Sprintf(`Create a TIMED EXPLANATORY MOTION sequence using cues, not a permanent title. Total narration duration is %.2f seconds; every cue must end by %.2f seconds to allow frame rounding. Preserve footage and audio. Match these editor-approved beats, interpreting percentages against total duration:
%s
Use only facts from this answer: %s
Keep cues at x=0.18–0.82 and y=0.20–0.61; captions will occupy y=0.70–0.84. White text #FFFFFF, cyan dots #70E4EA, yellow arrows #FFD166, blue drops #70B7FF. Headings centered at y=.25, size .085, at most 18 characters per line with actual newline for balanced headings; short subtitles at y=.32 size .055. Dots size .03, arrows .10, drops .085. Opening question at y=.28 size .09 must appear at time zero. Keep diagrams in y=.42–.60. Movement should explain the mechanism, not decorate it. Include every beat in at most 48 cues. No permanent title, invented facts, panels, or changes to the speech.`, duration, duration-.1, beats, ctx.Inputs.String("answer"))
	titled, err := ctx.RunPipeline("text-card", cookbook.Inputs{"text": ctx.Inputs.String("question"), "background_url": reel.URL("video_url"), "instructions": motionInstructions})
	if err != nil {
		return err
	}
	if err = ctx.Capture(5, titled.URL("video_url")); err != nil {
		return err
	}
	final, err := ctx.RunPipeline("captions", cookbook.Inputs{"source_asset_id": titled.URL("video_url"), "transcript_asset_id": transcript.URL("srt_asset_url"), "preset_name": "tiktok-bold-yellow", "position": "bottom"})
	if err != nil {
		return err
	}
	if err = ctx.Capture(6, final.URL("video_url")); err != nil {
		return err
	}
	ctx.SetOutput(final.URL("video_url"))
	return nil
}
