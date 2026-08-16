// Package hybrid routes execution between host and sandbox backends while
// keeping the host adapter as the only session catalog. The historical package
// name is retained to avoid churn at call sites; it no longer merges catalogs.
package hybrid

import (
	"context"
	"errors"
	"fmt"

	"github.com/rumpl/daw/internal/adapter"
	"github.com/rumpl/daw/internal/protocol"
)

type Adapter struct {
	host          adapter.Adapter
	sandbox       adapter.Adapter
	defaultTarget protocol.ExecutionTarget
}

func New(host, sandbox adapter.Adapter, defaultTarget protocol.ExecutionTarget) (*Adapter, error) {
	if host == nil || sandbox == nil {
		return nil, errors.New("target router requires host and sandbox adapters")
	}
	if defaultTarget != protocol.ExecutionTargetHost && defaultTarget != protocol.ExecutionTargetSandbox {
		return nil, fmt.Errorf("invalid default execution target %q", defaultTarget)
	}
	return &Adapter{host: host, sandbox: sandbox, defaultTarget: defaultTarget}, nil
}
func (a *Adapter) Info(ctx context.Context) (adapter.Info, error) { return a.host.Info(ctx) }
func (a *Adapter) ChatOptions(ctx context.Context, model string, servers []adapter.MCPServer) ([]protocol.ModelOption, []string, []protocol.ToolOption, error) {
	return a.host.ChatOptions(ctx, model, servers)
}

func (a *Adapter) ListSessions(ctx context.Context, workingDir string) ([]protocol.SessionSummary, error) {
	result, err := a.host.ListSessions(ctx, workingDir)
	if err != nil {
		return nil, err
	}
	for i := range result {
		target := targetFromAttributes(result[i].Attributes)
		result[i].ExecutionTarget = target
	}
	return result, nil
}

func (a *Adapter) ReadSession(ctx context.Context, sessionID string) (adapter.StoredSession, error) {
	target, err := a.targetForSession(ctx, sessionID)
	if err != nil {
		return adapter.StoredSession{}, err
	}
	stored, err := a.host.ReadSession(ctx, sessionID)
	if err == nil {
		stored.Meta.ExecutionTarget = target
	}
	return stored, err
}

func (a *Adapter) OpenChat(ctx context.Context, request adapter.OpenRequest) (adapter.Chat, error) {
	target := request.ExecutionTarget
	if request.ResumeSessionID != "" {
		persisted, err := a.targetForSession(ctx, request.ResumeSessionID)
		if err != nil {
			return nil, err
		}
		if target != "" && target != persisted {
			return nil, fmt.Errorf("%w: a session cannot change execution target", adapter.ErrUnsupported)
		}
		target = persisted
	} else if target == "" {
		target = a.defaultTarget
	}

	var selected adapter.Adapter
	switch target {
	case protocol.ExecutionTargetHost:
		selected = a.host
	case protocol.ExecutionTargetSandbox:
		selected = a.sandbox
	default:
		return nil, fmt.Errorf("%w: unknown execution target %q", adapter.ErrUnsupported, target)
	}
	request.ExecutionTarget = ""
	request.SessionAttributes = cloneAttributes(request.SessionAttributes)
	request.SessionAttributes[adapter.ExecutionTargetAttribute] = string(target)
	chat, err := selected.OpenChat(ctx, request)
	if err != nil {
		return nil, err
	}
	return &targetedChat{Chat: chat, target: target}, nil
}

func (a *Adapter) Close() error {
	// Stop runners before closing the host store they depend on.
	return errors.Join(a.sandbox.Close(), a.host.Close())
}

func (a *Adapter) targetForSession(ctx context.Context, sessionID string) (protocol.ExecutionTarget, error) {
	sessions, err := a.host.ListSessions(ctx, "")
	if err != nil {
		return "", err
	}
	for _, value := range sessions {
		if value.SessionID == sessionID {
			return targetFromAttributes(value.Attributes), nil
		}
	}
	return "", adapter.ErrNotFound
}

func targetFromAttributes(attributes map[string]string) protocol.ExecutionTarget {
	target := protocol.ExecutionTarget(attributes[adapter.ExecutionTargetAttribute])
	if target != protocol.ExecutionTargetSandbox {
		return protocol.ExecutionTargetHost
	}
	return target
}
func cloneAttributes(input map[string]string) map[string]string {
	output := make(map[string]string, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}

type targetedChat struct {
	adapter.Chat
	target protocol.ExecutionTarget
}

func (c *targetedChat) Meta() protocol.SessionMeta {
	meta := c.Chat.Meta()
	meta.Attributes = cloneAttributes(meta.Attributes)
	meta.Attributes[adapter.ExecutionTargetAttribute] = string(c.target)
	meta.ExecutionTarget = c.target
	return meta
}

var _ adapter.Adapter = (*Adapter)(nil)
