package orchestrator

import (
	"context"
	"io"

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

	config := &container.Config{
		Image: "worker:latest",
		Env: []string{
			"JOB_PAYLOAD=" + req.Payload,
		},
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
