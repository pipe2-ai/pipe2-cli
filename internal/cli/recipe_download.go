package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pipe2-ai/pipe2-cli/cookbook"
	"github.com/spf13/cobra"
)

// newRecipeDownloadCmd builds `pipe2 recipe download` — fetches every
// per-step artifact recorded in a --capture-to run's state.json into
// local files. Useful both ways: a user grabbing a recipe's
// intermediate + final outputs for editing/archival, and a maintainer
// collecting step artifacts to freeze as recipe-article samples.
func newRecipeDownloadCmd() *cobra.Command {
	var fromDir, toDir string
	c := &cobra.Command{
		Use:   "download",
		Short: "Download every step artifact from a --capture-to run",
		Long: `Download the per-step artifacts recorded in a recipe run's state.json.

A run started with --capture-to <dir> writes a state.json recording
each step's pipeline, run id, and output. download reads that file and
fetches every step's artifact:

  pipe2 recipe run clip-factory --input clip.mp4 --capture-to ./out
  pipe2 recipe download --from ./out

Files land as step-<n>-<pipeline>.<ext>. Use --to to write elsewhere.

Asset paths are resolved against the configured storage base — set it
once with 'pipe2 auth login --storage-url ...' or per-call via
$PIPE2_STORAGE_URL.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fromDir == "" {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--from <capture-dir> is required")}
			}
			if toDir == "" {
				toDir = fromDir
			}

			statePath := filepath.Join(fromDir, "state.json")
			raw, err := os.ReadFile(statePath)
			if err != nil {
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("read %s: %w", statePath, err)}
			}
			var state cookbook.ResumeState
			if err := json.Unmarshal(raw, &state); err != nil {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("parse %s: %w", statePath, err)}
			}
			if len(state.Steps) == 0 {
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("%s records no steps", statePath)}
			}

			cfg, _ := LoadConfig()
			storageBase := cfg.EffectiveStorageURL()
			if err := os.MkdirAll(toDir, 0o755); err != nil {
				return err
			}

			type downloaded struct {
				Idx      int    `json:"idx"`
				Pipeline string `json:"pipeline"`
				File     string `json:"file"`
				Bytes    int64  `json:"bytes"`
			}
			var out []downloaded
			for _, s := range state.Steps {
				rawURL := artifactURL(s.Output)
				if rawURL == "" {
					Status("step %d (%s): no artifact URL in output — skipped", s.Idx, s.Pipeline)
					continue
				}
				ext := strings.TrimPrefix(filepath.Ext(strings.SplitN(rawURL, "?", 2)[0]), ".")
				if ext == "" {
					ext = "bin"
				}
				name := fmt.Sprintf("step-%d-%s.%s", s.Idx, s.Pipeline, ext)
				dest := filepath.Join(toDir, name)
				n, err := cookbook.DownloadFile(cmd.Context(), cookbook.ResolveStorageURL(rawURL, storageBase), dest)
				if err != nil {
					return &ExitError{Code: ExitGeneric, Err: fmt.Errorf("step %d (%s): %w", s.Idx, s.Pipeline, err)}
				}
				Status("✓ step %d %s → %s (%d bytes)", s.Idx, s.Pipeline, name, n)
				out = append(out, downloaded{s.Idx, s.Pipeline, dest, n})
			}
			return Out(map[string]any{"recipe": state.Recipe, "downloaded": out})
		},
	}
	c.Flags().StringVar(&fromDir, "from", "", "capture directory containing state.json (required)")
	c.Flags().StringVar(&toDir, "to", "", "output directory (default: same as --from)")
	return c
}

// artifactURL picks the primary asset URL out of a pipeline's output
// map. Pipelines key their main artifact differently (video_url,
// srt_asset_url, …); first match in priority order wins.
func artifactURL(out map[string]any) string {
	for _, k := range []string{"video_url", "audio_url", "image_url", "srt_asset_url", "txt_asset_url", "url"} {
		if v, ok := out[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
