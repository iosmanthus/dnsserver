package request

import (
	"context"
	"math/rand"

	log "github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

var seed = atomic.NewUint64(rand.Uint64())

type ID uint64
type key string

func getKey() key {
	return "request_id"
}

func Gen() ID {
	return ID(seed.Inc() - 1)
}

func WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, getKey(), Gen())
}

func WithLogger(ctx context.Context, logger *log.Entry) *log.Entry {
	return logger.WithFields(log.Fields{
		string(getKey()): ctx.Value(getKey()),
	})
}

func Get(ctx context.Context) ID {
	id, ok := ctx.Value(getKey()).(ID)
	if !ok {
		return 0
	}
	return id
}
