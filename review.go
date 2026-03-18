package main

import "tetora/internal/review"

func buildReviewDigest(cfg *Config, days int) string {
	return review.BuildDigest(cfg, days)
}
