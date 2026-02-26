package gopi

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	gosdk "github.com/yangruihan/go-pi/pkg/sdk"
)

type Client struct {
	BinPath string
	Cwd     string
	sdk     *gosdk.Client
}

func New(binPath, cwd string) *Client {
	c := &Client{BinPath: strings.TrimSpace(binPath), Cwd: strings.TrimSpace(cwd)}
	if sdkClient, err := gosdk.New(gosdk.Options{CWD: c.Cwd, ContinueLatest: true}); err == nil {
		c.sdk = sdkClient
	}
	return c
}

func (c *Client) Ask(ctx context.Context, prompt string) (string, error) {
	if c.sdk != nil {
		return c.sdk.Ask(ctx, prompt)
	}
	if c.BinPath == "" {
		return "", fmt.Errorf("gopi binary path is required")
	}
	cmd := exec.CommandContext(ctx, c.BinPath, "--print")
	if c.Cwd != "" {
		cmd.Dir = c.Cwd
	}
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("invoke gopi failed: %s", msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func (c *Client) Close() error {
	if c.sdk != nil {
		return c.sdk.Close()
	}
	return nil
}
