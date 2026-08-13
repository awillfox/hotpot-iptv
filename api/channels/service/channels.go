package service

import (
	"context"

	"hotpot-iptv/internal/apperr"
	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/engine"
)

type ChannelRequest struct {
	Name          string `json:"name"`
	Number        int32  `json:"number"`
	Slug          string `json:"slug"`
	Enabled       *bool  `json:"enabled"`
	VideoWidth    int32  `json:"video_width"`
	VideoHeight   int32  `json:"video_height"`
	VideoBitrateK int32  `json:"video_bitrate_k"`
}

func validate(in ChannelRequest) error {
	fields := map[string]string{}
	if in.Name == "" {
		fields["name"] = "required"
	}
	if in.Number <= 0 {
		fields["number"] = "must be positive"
	}
	if len(fields) > 0 {
		return apperr.ValidationError{Fields: fields}
	}
	return nil
}

func (c Client) CreateChannel(ctx context.Context, in ChannelRequest) (channel.Channel, error) {
	if err := validate(in); err != nil {
		return channel.Channel{}, err
	}
	return c.app.Commands.Create.Handle(ctx, command.CreateInput{
		Name: in.Name, Number: in.Number, Slug: in.Slug,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
}

func (c Client) UpdateChannel(ctx context.Context, id int32, in ChannelRequest) (channel.Channel, error) {
	if err := validate(in); err != nil {
		return channel.Channel{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return c.app.Commands.Update.Handle(ctx, command.UpdateInput{
		ID: id, Name: in.Name, Number: in.Number, Slug: in.Slug, Enabled: enabled,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
}

func (c Client) ListChannels(ctx context.Context) ([]channel.Channel, error) {
	return c.app.Queries.List.Handle(ctx)
}

func (c Client) GetChannel(ctx context.Context, id int32) (channel.Channel, error) {
	return c.app.Queries.Get.Handle(ctx, id)
}

func (c Client) DeleteChannel(ctx context.Context, id int32) error {
	return c.app.Commands.Delete.Handle(ctx, id)
}

func (c Client) SetPlaylist(ctx context.Context, id int32, paths []string) ([]channel.PlaylistItem, error) {
	return c.app.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{ChannelID: id, Paths: paths})
}

func (c Client) GetPlaylist(ctx context.Context, id int32) ([]channel.PlaylistItem, error) {
	return c.app.Queries.GetPlaylist.Handle(ctx, id)
}

// ChannelWithStatus is a channel plus its live engine status. Status is nil
// when the channel is not running, so the JSON carries an explicit null.
type ChannelWithStatus struct {
	channel.Channel
	Status *engine.ChannelStatus `json:"status"`
}

// ListChannelsWithStatus enriches the list in one pass so the UI does not have
// to follow up with a status request per channel.
func (c Client) ListChannelsWithStatus(ctx context.Context) ([]ChannelWithStatus, error) {
	chs, err := c.app.Queries.List.Handle(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelWithStatus, 0, len(chs))
	for _, ch := range chs {
		item := ChannelWithStatus{Channel: ch}
		if st, ok := c.eng.Status(ch.ID); ok {
			item.Status = &st
		}
		out = append(out, item)
	}
	return out, nil
}

// StartChannel checks the channel exists first so a bad id is a 404 rather
// than a generic engine failure.
func (c Client) StartChannel(ctx context.Context, id int32) error {
	if _, err := c.app.Queries.Get.Handle(ctx, id); err != nil {
		return err
	}
	return c.eng.Start(ctx, id)
}

func (c Client) StopChannel(id int32) error { return c.eng.Stop(id) }

// ChannelStatus reports "stopped" rather than an error for a channel with no
// runner, so the UI has one shape to render.
func (c Client) ChannelStatus(id int32) engine.ChannelStatus {
	if st, ok := c.eng.Status(id); ok {
		return st
	}
	return engine.ChannelStatus{State: "stopped"}
}
