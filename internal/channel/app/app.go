package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/app/query"
	"hotpot-iptv/sqlc"
)

type Commands struct {
	Create      command.CreateHandler
	Update      command.UpdateHandler
	Delete      command.DeleteHandler
	SetPlaylist command.SetPlaylistHandler
}

type Queries struct {
	List        query.ListHandler
	Get         query.GetHandler
	GetPlaylist query.GetPlaylistHandler
}

type Application struct {
	Commands Commands
	Queries  Queries
}

func NewApplication(pool *pgxpool.Pool, prober command.Prober, mediaPath string) Application {
	q := sqlc.New(pool)
	return Application{
		Commands: Commands{
			Create:      command.NewCreateHandler(q),
			Update:      command.NewUpdateHandler(q),
			Delete:      command.NewDeleteHandler(q),
			SetPlaylist: command.NewSetPlaylistHandler(pool, q, prober, mediaPath),
		},
		Queries: Queries{
			List:        query.NewListHandler(q),
			Get:         query.NewGetHandler(q),
			GetPlaylist: query.NewGetPlaylistHandler(q),
		},
	}
}
