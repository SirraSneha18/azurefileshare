package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/nektos/act/common"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh/terminal"
)

// NewDockerRunExecutorInput the input for the NewDockerRunExecutor function
type NewDockerRunExecutorInput struct {
	DockerExecutorInput
	Image      string
	Entrypoint string
	Cmd        []string
	WorkingDir string
	Env        []string
	Binds      []string
	Content    map[string]io.Reader
	Volumes    []string
	Name       string
}

// NewDockerRunExecutor function to create a run executor for the container
func NewDockerRunExecutor(input NewDockerRunExecutorInput) common.Executor {
	return func() error {

		input.Logger.Infof("docker run %s %s", input.Image, input.Cmd)
		if input.Dryrun {
			return nil
		}

		cli, err := client.NewClientWithOpts()
		if err != nil {
			return err
		}

		containerID, err := createContainer(input, cli)
		if err != nil {
			return err
		}
		defer removeContainer(input, cli, containerID)

		err = copyContentToContainer(input, cli, containerID)
		if err != nil {
			return err
		}

		err = attachContainer(input, cli, containerID)
		if err != nil {
			return err
		}

		err = startContainer(input, cli, containerID)
		if err != nil {
			return err
		}

		return waitContainer(input, cli, containerID)

	}

}

func createContainer(input NewDockerRunExecutorInput, cli *client.Client) (string, error) {
	isTerminal := terminal.IsTerminal(int(os.Stdout.Fd()))

	cmd := input.Cmd
	if len(input.Cmd) == 1 {
		cmd = strings.Split(cmd[0], " ")
	}

	config := &container.Config{
		Image:      input.Image,
		Cmd:        cmd,
		WorkingDir: input.WorkingDir,
		Env:        input.Env,
		Tty:        isTerminal,
	}

	if len(input.Volumes) > 0 {
		config.Volumes = make(map[string]struct{})
		for _, vol := range input.Volumes {
			config.Volumes[vol] = struct{}{}
		}
	}

	if input.Entrypoint != "" {
		config.Entrypoint = []string{input.Entrypoint}
	}
	resp, err := cli.ContainerCreate(input.Ctx, config, &container.HostConfig{
		Binds: input.Binds,
	}, nil, input.Name)
	if err != nil {
		return "", err
	}
	input.Logger.Debugf("Created container name=%s id=%v from image %v", input.Name, resp.ID, input.Image)
	log.Debugf("ENV ==> %v", input.Env)

	return resp.ID, nil
}

func removeContainer(input NewDockerRunExecutorInput, cli *client.Client, containerID string) {
	err := cli.ContainerRemove(context.Background(), containerID, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
	if err != nil {
		input.Logger.Errorf("%v", err)
	}

	input.Logger.Debugf("Removed container: %v", containerID)
}

func copyContentToContainer(input NewDockerRunExecutorInput, cli *client.Client, containerID string) error {
	for dstPath, srcReader := range input.Content {
		input.Logger.Debugf("Extracting content to '%s'", dstPath)
		err := cli.CopyToContainer(input.Ctx, containerID, dstPath, srcReader, types.CopyToContainerOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func attachContainer(input NewDockerRunExecutorInput, cli *client.Client, containerID string) error {
	out, err := cli.ContainerAttach(input.Ctx, containerID, types.ContainerAttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return err
	}
	isTerminal := terminal.IsTerminal(int(os.Stdout.Fd()))
	if !isTerminal || os.Getenv("NORAW") != "" {
		go input.logDockerOutput(out.Reader)
	} else {
		go input.streamDockerOutput(out.Reader)
	}
	return nil
}

func startContainer(input NewDockerRunExecutorInput, cli *client.Client, containerID string) error {
	input.Logger.Debugf("STARTING image=%s entrypoint=%s cmd=%v", input.Image, input.Entrypoint, input.Cmd)

	if err := cli.ContainerStart(input.Ctx, containerID, types.ContainerStartOptions{}); err != nil {
		return err
	}

	input.Logger.Debugf("Started container: %v", containerID)
	return nil
}

func waitContainer(input NewDockerRunExecutorInput, cli *client.Client, containerID string) error {
	statusCh, errCh := cli.ContainerWait(input.Ctx, containerID, container.WaitConditionNotRunning)
	var statusCode int64
	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case status := <-statusCh:
		statusCode = status.StatusCode
	}

	input.Logger.Debugf("Return status: %v", statusCode)

	if statusCode == 0 {
		return nil
	} else if statusCode == 78 {
		return fmt.Errorf("exiting with `NEUTRAL`: 78")
	}

	return fmt.Errorf("exit with `FAILURE`: %v", statusCode)
}
