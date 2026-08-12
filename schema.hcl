schema "public" {}

table "channels" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "name" {
    null = false
    type = text
  }
  column "number" {
    null = false
    type = integer
  }
  column "slug" {
    null = false
    type = text
  }
  column "enabled" {
    null    = false
    type    = boolean
    default = true
  }
  column "video_width" {
    null    = false
    type    = integer
    default = 1920
  }
  column "video_height" {
    null    = false
    type    = integer
    default = 1080
  }
  column "video_bitrate_k" {
    null    = false
    type    = integer
    default = 5000
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  column "deleted_at" {
    null = true
    type = timestamptz
  }
  primary_key {
    columns = [column.id]
  }
  index "channels_slug_key" {
    unique  = true
    columns = [column.slug]
  }
  index "channels_number_key" {
    unique  = true
    columns = [column.number]
  }
}

table "playlist_items" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "channel_id" {
    null = false
    type = integer
  }
  column "position" {
    null = false
    type = integer
  }
  column "path" {
    null = false
    type = text
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "playlist_items_channel_id_fkey" {
    columns     = [column.channel_id]
    ref_columns = [table.channels.column.id]
    on_delete   = CASCADE
  }
  index "playlist_items_channel_pos_idx" {
    columns = [column.channel_id, column.position]
  }
}

table "media_files" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "path" {
    null = false
    type = text
  }
  column "size" {
    null = false
    type = bigint
  }
  column "mtime" {
    null = false
    type = timestamptz
  }
  column "duration_ms" {
    null = false
    type = bigint
  }
  column "video_codec" {
    null = false
    type = text
  }
  column "probe" {
    null = false
    type = jsonb
  }
  column "probed_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  index "media_files_path_key" {
    unique  = true
    columns = [column.path]
  }
}

table "channel_state" {
  schema = schema.public
  column "channel_id" {
    null = false
    type = integer
  }
  column "item_position" {
    null    = false
    type    = integer
    default = 0
  }
  column "item_started_at" {
    null = true
    type = timestamptz
  }
  column "status" {
    null    = false
    type    = text
    default = "stopped"
  }
  column "last_error" {
    null = true
    type = text
  }
  column "updated_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.channel_id]
  }
  foreign_key "channel_state_channel_id_fkey" {
    columns     = [column.channel_id]
    ref_columns = [table.channels.column.id]
    on_delete   = CASCADE
  }
}

table "channel_events" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "channel_id" {
    null = false
    type = integer
  }
  column "level" {
    null = false
    type = text
  }
  column "message" {
    null = false
    type = text
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "channel_events_channel_id_fkey" {
    columns     = [column.channel_id]
    ref_columns = [table.channels.column.id]
    on_delete   = CASCADE
  }
  index "channel_events_channel_idx" {
    columns = [column.channel_id, column.id]
  }
}
