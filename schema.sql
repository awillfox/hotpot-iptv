-- Add new schema named "public"
CREATE SCHEMA IF NOT EXISTS "public";
-- Set comment to schema: "public"
COMMENT ON SCHEMA "public" IS 'standard public schema';
-- Create "channels" table
CREATE TABLE "public"."channels" (
  "id" serial NOT NULL,
  "name" text NOT NULL,
  "number" integer NOT NULL,
  "slug" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT true,
  "video_width" integer NOT NULL DEFAULT 1920,
  "video_height" integer NOT NULL DEFAULT 1080,
  "video_bitrate_k" integer NOT NULL DEFAULT 5000,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "deleted_at" timestamptz NULL,
  "source_folder" text NULL,
  PRIMARY KEY ("id")
);
-- Create index "channels_number_key" to table: "channels"
CREATE UNIQUE INDEX "channels_number_key" ON "public"."channels" ("number");
-- Create index "channels_slug_key" to table: "channels"
CREATE UNIQUE INDEX "channels_slug_key" ON "public"."channels" ("slug");
-- Create "media_files" table
CREATE TABLE "public"."media_files" (
  "id" serial NOT NULL,
  "path" text NOT NULL,
  "size" bigint NOT NULL,
  "mtime" timestamptz NOT NULL,
  "duration_ms" bigint NOT NULL,
  "video_codec" text NOT NULL,
  "probe" jsonb NOT NULL,
  "probed_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "media_files_path_key" to table: "media_files"
CREATE UNIQUE INDEX "media_files_path_key" ON "public"."media_files" ("path");
-- Create "channel_events" table
CREATE TABLE "public"."channel_events" (
  "id" serial NOT NULL,
  "channel_id" integer NOT NULL,
  "level" text NOT NULL,
  "message" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "channel_events_channel_id_fkey" FOREIGN KEY ("channel_id") REFERENCES "public"."channels" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "channel_events_channel_idx" to table: "channel_events"
CREATE INDEX "channel_events_channel_idx" ON "public"."channel_events" ("channel_id", "id");
-- Create "channel_state" table
CREATE TABLE "public"."channel_state" (
  "channel_id" integer NOT NULL,
  "item_position" integer NOT NULL DEFAULT 0,
  "item_started_at" timestamptz NULL,
  "status" text NOT NULL DEFAULT 'stopped',
  "last_error" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("channel_id"),
  CONSTRAINT "channel_state_channel_id_fkey" FOREIGN KEY ("channel_id") REFERENCES "public"."channels" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create "playlist_items" table
CREATE TABLE "public"."playlist_items" (
  "id" serial NOT NULL,
  "channel_id" integer NOT NULL,
  "position" integer NOT NULL,
  "path" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "playlist_items_channel_id_fkey" FOREIGN KEY ("channel_id") REFERENCES "public"."channels" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "playlist_items_channel_pos_idx" to table: "playlist_items"
CREATE INDEX "playlist_items_channel_pos_idx" ON "public"."playlist_items" ("channel_id", "position");
