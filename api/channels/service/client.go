package service

import (
	"context"

	"hotpot-iptv/internal/channel/app"
	"hotpot-iptv/internal/engine"
)

// Engine is the slice of the supervisor this API needs. Keeping it an
// interface here means the HTTP layer can be tested without running ffmpeg.
type Engine interface {
	Start(ctx context.Context, channelID int32) error
	Stop(channelID int32) error
	Status(channelID int32) (engine.ChannelStatus, bool)
}

type Client struct {
	app app.Application
	eng Engine
}

func NewClient(a app.Application, eng Engine) Client { return Client{app: a, eng: eng} }
