package llm

import (
	"context"
	"sync"
)

type Usage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

func (usage Usage) Total() int64 { return usage.Input + usage.Output }

type Meter struct {
	mutex sync.Mutex
	usage Usage
}

func (meter *Meter) Usage() Usage {
	meter.mutex.Lock()
	defer meter.mutex.Unlock()
	return meter.usage
}

type meterKey struct{}

func WithMeter(ctx context.Context, meter *Meter) context.Context {
	return context.WithValue(ctx, meterKey{}, meter)
}

// Providers call Report once per response; without a meter on ctx it is a no-op.
func Report(ctx context.Context, usage Usage) {
	meter, _ := ctx.Value(meterKey{}).(*Meter)
	if meter == nil {
		return
	}
	meter.mutex.Lock()
	defer meter.mutex.Unlock()
	meter.usage.Input += usage.Input
	meter.usage.Output += usage.Output
}

func UsageFrom(ctx context.Context) Usage {
	if meter, _ := ctx.Value(meterKey{}).(*Meter); meter != nil {
		return meter.Usage()
	}
	return Usage{}
}
