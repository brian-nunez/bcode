package orchestrator

import (
	"context"
	"io"
	"os"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

type JobRequest struct {
	Payload string
}

func RunJob(ctx context.Context, req JobRequest) (io.ReadCloser, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}

	workerImage := os.Getenv("WORKER_IMAGE")
	if workerImage == "" {
		workerImage = "bbaas-worker:latest"
	}

	containerEnv := []string{
		"JOB_PAYLOAD=" + req.Payload,
	}

	if ollamaModel := os.Getenv("OLLAMA_MODEL"); ollamaModel != "" {
		containerEnv = append(containerEnv, "OLLAMA_MODEL="+ollamaModel)
	}
	if ollamaEndpoint := os.Getenv("OLLAMA_ENDPOINT"); ollamaEndpoint != "" {
		containerEnv = append(containerEnv, "OLLAMA_ENDPOINT="+ollamaEndpoint)
	}

	config := &container.Config{
		Image: workerImage,
		Env:   containerEnv,
	}

	hostConfig := &container.HostConfig{
		AutoRemove: true,
	}

	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     config,
		HostConfig: hostConfig,
	})
	if err != nil {
		return nil, err
	}

	if _, err := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return nil, err
	}

	// Monitor context cancellation to kill container on client disconnect
	go func() {
		<-ctx.Done()
		// Use a background context because the original 'ctx' is already dead
		cli.ContainerRemove(context.Background(), resp.ID, client.ContainerRemoveOptions{Force: true})
	}()

	return cli.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
}
