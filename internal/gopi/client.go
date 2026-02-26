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
	info    RuntimeInfo
}

type RuntimeInfo struct {
	Mode         string
	Provider     string
	Model        string
	ConfigModel  string
	SessionModel string
	Host         string
	APIBase      string
	CWD          string
	SessionID    string
	ConfigPaths  []string
}

func New(binPath, cwd string) *Client {
	c := &Client{BinPath: strings.TrimSpace(binPath), Cwd: strings.TrimSpace(cwd)}
	if sdkClient, err := gosdk.New(gosdk.Options{CWD: c.Cwd, ContinueLatest: true, PreferConfigModel: true}); err == nil {
		c.sdk = sdkClient
		si := sdkClient.Info()
		c.info = RuntimeInfo{
			Mode:         si.Mode,
			Provider:     si.Provider,
			Model:        si.Model,
			ConfigModel:  si.ConfigModel,
			SessionModel: si.SessionModel,
			Host:         si.Host,
			APIBase:      si.APIBase,
			CWD:          si.CWD,
			SessionID:    si.SessionID,
			ConfigPaths:  append([]string(nil), si.ConfigPaths...),
		}
	} else {
		c.info = RuntimeInfo{
			Mode:         "binary-fallback",
			CWD:          c.Cwd,
			Provider:     "(由 gopi 二进制决定)",
			Model:        "(由 gopi 二进制决定)",
			ConfigModel:  "(由 gopi 二进制决定)",
			SessionModel: "(由 gopi 二进制决定)",
			Host:         "(由 gopi 二进制决定)",
			APIBase:      "(由 gopi 二进制决定)",
			SessionID:    "(由 gopi 二进制决定)",
		}
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

func (c *Client) AskWithStats(ctx context.Context, prompt string) (string, int, int, error) {
	if c.sdk != nil {
		text, meta, err := c.sdk.AskWithMeta(ctx, prompt)
		if err != nil {
			return "", 0, 0, err
		}
		return text, meta.ToolCallCount(), meta.WriteToolCallCount(), nil
	}

	text, err := c.Ask(ctx, prompt)
	if err != nil {
		return "", 0, 0, err
	}
	return text, 0, 0, nil
}

func (c *Client) Close() error {
	if c.sdk != nil {
		return c.sdk.Close()
	}
	return nil
}

func (c *Client) Info() RuntimeInfo {
	out := c.info
	out.ConfigPaths = append([]string(nil), c.info.ConfigPaths...)
	return out
}
