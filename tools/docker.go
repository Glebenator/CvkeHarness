package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ensure docker client is available, lazily instantiate to prevent startup crashes if docker is down
func getDockerClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// DockerListTool lists all containers.
type DockerListTool struct{}

func NewDockerListTool() *DockerListTool {
	return &DockerListTool{}
}

func (d *DockerListTool) Name() string { return "docker_list_containers" }
func (d *DockerListTool) Description() string {
	return "Lists all Docker containers on the host, including stopped ones."
}
func (d *DockerListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (d *DockerListTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	cli, err := getDockerClient()
	if err != nil {
		return "", fmt.Errorf("failed to connect to docker: %w", err)
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	var results []string
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = c.Names[0]
		}
		results = append(results, fmt.Sprintf("ID: %s, Name: %s, Image: %s, State: %s, Status: %s",
			c.ID[:12], name, c.Image, c.State, c.Status))
	}

	if len(results) == 0 {
		return "No containers found.", nil
	}

	res, _ := json.MarshalIndent(results, "", "  ")
	return string(res), nil
}

// DockerInspectTool inspects a single container
type DockerInspectTool struct{}

type DockerInspectArgs struct {
	Container string `json:"container"`
}

func NewDockerInspectTool() *DockerInspectTool { return &DockerInspectTool{} }

func (d *DockerInspectTool) Name() string { return "docker_inspect_container" }
func (d *DockerInspectTool) Description() string {
	return "Gets detailed inspection information for a specific Docker container (by name or ID)."
}
func (d *DockerInspectTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"container": { "type": "string", "description": "Container Name or ID" }
		},
		"required": ["container"]
	}`)
}

func (d *DockerInspectTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var parsed DockerInspectArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "", err
	}

	cli, err := getDockerClient()
	if err != nil {
		return "", err
	}
	defer cli.Close()

	inspect, _, err := cli.ContainerInspectWithRaw(ctx, parsed.Container, true)
	if err != nil {
		return "", fmt.Errorf("failed to inspect container %s: %w", parsed.Container, err)
	}

	// Only return the most relevant parts so we don't blow up the LLM token context
	summary := map[string]interface{}{
		"ID":      inspect.ID,
		"Name":    inspect.Name,
		"State":   inspect.State,
		"Image":   inspect.Config.Image,
		"Network": inspect.NetworkSettings.Networks,
		"Mounts":  inspect.Mounts,
	}

	result, _ := json.MarshalIndent(summary, "", "  ")
	return string(result), nil
}

// DockerRestartTool restarts a container
type DockerRestartTool struct{}

func NewDockerRestartTool() *DockerRestartTool { return &DockerRestartTool{} }
func (d *DockerRestartTool) Name() string      { return "docker_restart_container" }
func (d *DockerRestartTool) Description() string {
	return "Restarts a specific Docker container (by name or ID)."
}
func (d *DockerRestartTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"container": { "type": "string", "description": "Container Name or ID" }
		},
		"required": ["container"]
	}`)
}

func (d *DockerRestartTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var parsed DockerInspectArgs // reuse args struct
	if err := json.Unmarshal(args, &parsed); err != nil {
		return "", err
	}

	cli, err := getDockerClient()
	if err != nil {
		return "", err
	}
	defer cli.Close()

	timeout := 10 // seconds
	err = cli.ContainerRestart(ctx, parsed.Container, container.StopOptions{Timeout: &timeout})
	if err != nil {
		return "", fmt.Errorf("failed to restart container %s: %w", parsed.Container, err)
	}

	return fmt.Sprintf("Successfully restarted container: %s", parsed.Container), nil
}
