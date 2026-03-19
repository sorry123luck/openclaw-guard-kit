package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/logging"
)

const DefaultPipeName = `\\.\pipe\openclaw-guard`

type Publisher interface {
	Publish(context.Context, protocol.Event) error
}

// RequestHandler 供后面的命名管道服务端调用。
// 前端/agent 通过 gateway 发来的消息，最终会交给这里处理。
type RequestHandler interface {
	HandleMessage(context.Context, protocol.Message) (protocol.Message, error)
}

// PipeConfig 把 gateway 的主管道信息先定死。
// 现在先把结构固定下来，后面 server/client 直接复用。
type PipeConfig struct {
	PipeName string
	StopFunc func()
}

func (c PipeConfig) ResolvePipeName() string {
	name := strings.TrimSpace(c.PipeName)
	if name == "" {
		return DefaultPipeName
	}
	return name
}

// ScopePipeName 后面如果你要做不同作用域管道，可以直接复用这个方法。
// 当前第一阶段先统一走主管道，不强制使用分作用域。
func ScopePipeName(baseName, scope string) string {
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		baseName = DefaultPipeName
	}

	scope = strings.TrimSpace(scope)
	if scope == "" {
		return baseName
	}

	cleanScope := sanitizePipeSegment(scope)
	return fmt.Sprintf("%s-%s", baseName, cleanScope)
}

type MemoryPublisher struct {
	logger *logging.Logger
	mu     sync.Mutex
	events []protocol.Event
}

func NewMemoryPublisher(logger *logging.Logger) *MemoryPublisher {
	return &MemoryPublisher{logger: logger}
}

func (p *MemoryPublisher) Publish(_ context.Context, event protocol.Event) error {
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()

	raw, _ := json.Marshal(event)
	p.logger.Debug("gateway published event", "event", string(raw))
	return nil
}

func (p *MemoryPublisher) Events() []protocol.Event {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]protocol.Event, len(p.events))
	copy(out, p.events)
	return out
}

func sanitizePipeSegment(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}

	replacer := strings.NewReplacer(
		"\\", "-",
		"/", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
		" ", "-",
	)
	v = replacer.Replace(v)
	for strings.Contains(v, "--") {
		v = strings.ReplaceAll(v, "--", "-")
	}
	v = strings.Trim(v, "-")
	if v == "" {
		return "default"
	}
	return v
}
