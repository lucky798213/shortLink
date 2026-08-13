package app

import "short_url/internal/shortlink"

type LinkResult struct {
	Link   *shortlink.Link
	Status shortlink.Status
}

type StatsResult struct {
	Stats  *shortlink.Stats
	Status shortlink.Status
}
