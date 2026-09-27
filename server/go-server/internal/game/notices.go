package game

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

// Claim before delivery: a crashed dispatch is never replayed to new logins.
func (h *Hub) RunNotices(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.dispatchNotice(ctx); err != nil && ctx.Err() == nil {
				log.Printf("gm_notice_error: %v", err)
			}
		}
	}
}
func (h *Hub) dispatchNotice(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var id, text string
	var created int64
	err := h.Store.DB.QueryRowContext(ctx, "SELECT id,content,created FROM gm_notices WHERE state='pending' ORDER BY created LIMIT 1").Scan(&id, &text, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	state := "dispatching"
	if time.Now().Unix()-created > 60 {
		state = "expired"
	}
	result, err := h.Store.DB.ExecContext(ctx, "UPDATE gm_notices SET state=? WHERE id=? AND state='pending'", state, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 || state == "expired" {
		return nil
	}
	count := h.sendNotice(text)
	_, err = h.Store.DB.ExecContext(ctx, "UPDATE gm_notices SET state='sent',recipients=? WHERE id=?", count, id)
	log.Printf("gm_notice id=%q queued=%d", id, count)
	return err
}
func (h *Hub) sendNotice(text string) int {
	h.lockState()
	defer h.unlockState()
	count := 0
	for _, s := range h.Sessions {
		if s.LoggedOut || s.game() == nil {
			continue
		}
		select {
		case <-s.Done:
			continue
		default:
		}
		s.sendGame(notice(text))
		select {
		case <-s.Done:
			continue
		default:
			count++
		}
	}
	return count
}
