package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/semaphoreio/agent-cli/pkg/client"
	"github.com/semaphoreio/agent-cli/pkg/config"
	"github.com/semaphoreio/agent-cli/pkg/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// --- pipeline topology subcommand ---

var pipelineTopologyCmd = &cobra.Command{
	Use:   "topology <pipeline-id>",
	Short: "Show block dependency graph for a pipeline",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent pipeline topology <pipeline-id>
  sem-agent pipeline topology <pipeline-id> --format table`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		topo, err := fetchTopology(args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}
		output.Result(topo)
		return nil
	},
}

// --- critical-path standalone command ---

var criticalPathCmd = &cobra.Command{
	Use:   "critical-path <pipeline-id>",
	Short: "Show the longest dependency chain (bottleneck) in a pipeline",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent critical-path <pipeline-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}
		topo, err := fetchTopology(args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		blocks, _ := topo["blocks"].([]blockTopology)
		if blocks == nil {
			// try interface conversion
			rawBlocks, _ := topo["blocks"].([]interface{})
			if len(rawBlocks) == 0 {
				output.Result(map[string]any{
					"pipeline_id":   args[0],
					"critical_path": []string{},
					"message":       "no blocks found",
				})
				return nil
			}
		}

		blocksTyped := topoBlocksFromMap(topo)
		if len(blocksTyped) == 0 {
			output.Result(map[string]any{
				"pipeline_id":   args[0],
				"critical_path": []string{},
				"message":       "no blocks found",
			})
			return nil
		}

		critPath := computeCriticalPath(blocksTyped)

		output.Result(map[string]any{
			"pipeline_id":   args[0],
			"critical_path": critPath,
			"depth":         len(critPath),
			"total_blocks":  len(blocksTyped),
		})
		return nil
	},
}

// --- blast-radius standalone command ---

var blastRadiusBlockFlag string

var blastRadiusCmd = &cobra.Command{
	Use:   "blast-radius <pipeline-id>",
	Short: "Show which blocks were affected by failures in a pipeline",
	Args:  cobra.ExactArgs(1),
	Example: `  sem-agent blast-radius <pipeline-id>
  sem-agent blast-radius <pipeline-id> --block "Build project"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsConfigured() {
			return fmt.Errorf("not configured — run 'sem-agent connect' first")
		}

		topo, err := fetchTopology(args[0])
		if err != nil {
			output.Error("api_error", err.Error(), 1)
			return err
		}

		blocksTyped := topoBlocksFromMap(topo)
		blockMap := map[string]*blockTopology{}
		for i := range blocksTyped {
			blockMap[blocksTyped[i].Name] = &blocksTyped[i]
		}

		rootFailures, cascading := computeBlastRadius(blocksTyped)

		// If --block specified, show blast radius for that specific block
		if blastRadiusBlockFlag != "" {
			var affected []string
			visited := map[string]bool{}
			queue := []string{blastRadiusBlockFlag}
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				for _, bt := range blocksTyped {
					if visited[bt.Name] {
						continue
					}
					for _, dep := range bt.Dependencies {
						if dep == current {
							affected = append(affected, bt.Name)
							visited[bt.Name] = true
							queue = append(queue, bt.Name)
							break
						}
					}
				}
			}

			output.Result(map[string]any{
				"pipeline_id":     args[0],
				"source_block":    blastRadiusBlockFlag,
				"affected_blocks": affected,
				"affected_count":  len(affected),
			})
			return nil
		}

		if rootFailures == nil {
			rootFailures = []string{}
		}
		if cascading == nil {
			cascading = []string{}
		}

		output.Result(map[string]any{
			"pipeline_id":     args[0],
			"root_failures":   rootFailures,
			"cascading":       cascading,
			"total_blocks":    len(blockMap),
			"total_failed":    len(rootFailures),
			"total_cascading": len(cascading),
		})
		return nil
	},
}

// blockTopology holds block info with dependencies and runtime state.
type blockTopology struct {
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Result       string   `json:"result"`
	Dependencies []string `json:"dependencies"`
}

// computeCriticalPath finds the longest dependency chain in a set of blocks.
func computeCriticalPath(blocks []blockTopology) []string {
	blockMap := map[string]*blockTopology{}
	for i := range blocks {
		blockMap[blocks[i].Name] = &blocks[i]
	}

	memo := map[string][]string{}
	var longestPath func(name string) []string
	longestPath = func(name string) []string {
		if cached, ok := memo[name]; ok {
			return cached
		}
		bi := blockMap[name]
		if bi == nil || len(bi.Dependencies) == 0 {
			memo[name] = []string{name}
			return memo[name]
		}
		var best []string
		for _, dep := range bi.Dependencies {
			p := longestPath(dep)
			if len(p) > len(best) {
				best = p
			}
		}
		result := make([]string, len(best)+1)
		copy(result, best)
		result[len(best)] = name
		memo[name] = result
		return result
	}

	var critPath []string
	for _, bi := range blocks {
		p := longestPath(bi.Name)
		if len(p) > len(critPath) {
			critPath = p
		}
	}
	return critPath
}

// computeBlastRadius identifies root failures and cascading blocks.
func computeBlastRadius(blocks []blockTopology) (rootFailures, cascading []string) {
	blockMap := map[string]*blockTopology{}
	for i := range blocks {
		blockMap[blocks[i].Name] = &blocks[i]
	}

	for _, bt := range blocks {
		if bt.Result != "failed" {
			continue
		}
		allDepsPassed := true
		for _, dep := range bt.Dependencies {
			if depBt, ok := blockMap[dep]; ok {
				if depBt.Result != "passed" {
					allDepsPassed = false
					break
				}
			}
		}
		if allDepsPassed {
			rootFailures = append(rootFailures, bt.Name)
		}
	}

	for _, bt := range blocks {
		if bt.Result == "stopped" || bt.Result == "canceled" || bt.Result == "" {
			for _, dep := range bt.Dependencies {
				if depBt, ok := blockMap[dep]; ok {
					if depBt.Result == "failed" || depBt.Result == "stopped" || depBt.Result == "canceled" {
						cascading = append(cascading, bt.Name)
						break
					}
				}
			}
		}
	}

	return rootFailures, cascading
}

// fetchTopology builds topology by combining pipeline show (v1alpha, block states)
// with the pipeline YAML (block dependencies). Falls back to v2 if available.
func fetchTopology(pipelineID string) (map[string]interface{}, error) {
	c := client.New()

	// 1. Get pipeline details (blocks with state/result)
	params := url.Values{}
	params.Set("detailed", "true")
	pplResp, err := c.ListWithParams("pipelines/"+pipelineID, params)
	if err != nil {
		return nil, err
	}
	if pplResp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", pplResp.StatusCode, string(pplResp.Body))
	}

	var pplData struct {
		Pipeline struct {
			PplID        string `json:"ppl_id"`
			Name         string `json:"name"`
			YAMLFile     string `json:"yaml_file_name"`
			WorkingDir   string `json:"working_directory"`
			ProjectID    string `json:"project_id"`
			WfID         string `json:"wf_id"`
			CommitSHA    string `json:"commit_sha"`
		} `json:"pipeline"`
		Blocks []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Result string `json:"result"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(pplResp.Body, &pplData); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline: %w", err)
	}

	// Build block state map
	blockStates := map[string]struct{ State, Result string }{}
	for _, b := range pplData.Blocks {
		blockStates[b.Name] = struct{ State, Result string }{b.State, b.Result}
	}

	// 2. Try to get YAML from artifacts (job logs contain the YAML path)
	//    Simplest: fetch the YAML via the workflow's artifact
	yamlFile := pplData.Pipeline.YAMLFile
	if yamlFile == "" {
		yamlFile = "semaphore.yml"
	}
	workingDir := pplData.Pipeline.WorkingDir
	if workingDir == "" {
		workingDir = ".semaphore"
	}
	artifactPath := fmt.Sprintf("%s/%s", workingDir, yamlFile)

	// Try fetching YAML from workflow artifacts
	wfID := pplData.Pipeline.WfID
	var yamlDeps map[string][]string // block name → dependencies

	if wfID != "" {
		artParams := url.Values{}
		artParams.Set("scope", "workflows")
		artParams.Set("scope_id", wfID)
		artParams.Set("path", artifactPath)
		artResp, err := c.ListWithParams("artifacts/signed_url", artParams)
		if err == nil && artResp.StatusCode == 200 {
			var artData struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(artResp.Body, &artData) == nil && artData.URL != "" {
				yamlResp, err := c.GetExternal(artData.URL)
				if err == nil && yamlResp.StatusCode == 200 {
					yamlDeps = parseYAMLDependencies(yamlResp.Body)
				}
			}
		}
	}

	// 3. If artifacts didn't work, try v2 as fallback for dependency info
	if yamlDeps == nil {
		_ = c.ResolveOrgID()
		v2Resp, err := c.GetVersioned("v2", "pipelines/"+pipelineID, "describe_topology")
		if err == nil && v2Resp.StatusCode == 200 {
			var v2Data struct {
				Blocks []struct {
					Name         string   `json:"name"`
					Dependencies []string `json:"dependencies"`
				} `json:"blocks"`
			}
			if json.Unmarshal(v2Resp.Body, &v2Data) == nil && len(v2Data.Blocks) > 0 {
				yamlDeps = map[string][]string{}
				for _, b := range v2Data.Blocks {
					if b.Dependencies != nil {
						yamlDeps[b.Name] = b.Dependencies
					} else {
						yamlDeps[b.Name] = []string{}
					}
				}
			}
		}
	}

	// 4. If still no deps, try to infer from block ordering (basic heuristic):
	//    blocks without explicit deps depend on nothing; otherwise use YAML data
	if yamlDeps == nil {
		// Last resort: infer from execution order. Blocks that are "waiting"
		// while others run are likely dependent. But this is imprecise.
		// Return blocks without dependency info.
		yamlDeps = map[string][]string{}
	}

	// Build topology result
	blocks := make([]blockTopology, 0, len(pplData.Blocks))
	for _, b := range pplData.Blocks {
		bt := blockTopology{
			Name:         b.Name,
			State:        b.State,
			Result:       b.Result,
			Dependencies: yamlDeps[b.Name],
		}
		if bt.Dependencies == nil {
			bt.Dependencies = []string{}
		}
		blocks = append(blocks, bt)
	}

	result := map[string]interface{}{
		"pipeline_id": pipelineID,
		"blocks":      blocks,
		"total":       len(blocks),
		"source":      "v1alpha+yaml",
	}

	return result, nil
}

// parseYAMLDependencies extracts block dependency info from a Semaphore pipeline YAML.
func parseYAMLDependencies(yamlContent []byte) map[string][]string {
	var pipeline struct {
		Blocks []struct {
			Name         string   `yaml:"name"`
			Dependencies []string `yaml:"dependencies"`
		} `yaml:"blocks"`
	}

	if err := yaml.Unmarshal(yamlContent, &pipeline); err != nil {
		return nil
	}

	if len(pipeline.Blocks) == 0 {
		return nil
	}

	deps := map[string][]string{}
	for _, b := range pipeline.Blocks {
		if b.Dependencies != nil {
			deps[b.Name] = b.Dependencies
		} else {
			deps[b.Name] = []string{}
		}
	}
	return deps
}

// topoBlocksFromMap extracts []blockTopology from the topology result map.
func topoBlocksFromMap(topo map[string]interface{}) []blockTopology {
	// Try direct typed assertion first
	if typed, ok := topo["blocks"].([]blockTopology); ok {
		return typed
	}

	// Re-marshal and unmarshal
	raw, err := json.Marshal(topo["blocks"])
	if err != nil {
		return nil
	}
	var blocks []blockTopology
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	return blocks
}

func init() {
	blastRadiusCmd.Flags().StringVar(&blastRadiusBlockFlag, "block", "", "show blast radius for a specific block")

	pipelineCmd.AddCommand(pipelineTopologyCmd)
	rootCmd.AddCommand(criticalPathCmd)
	rootCmd.AddCommand(blastRadiusCmd)
}
