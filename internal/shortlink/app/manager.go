package app

import (
	"context"
	"fmt"
)

type Manager struct {
	store    LinkStore
	resolver *Resolver
}

func newManager(store LinkStore, resolver *Resolver) *Manager {
	return &Manager{store: store, resolver: resolver}
}

func (m *Manager) Delete(ctx context.Context, shortCode string) (bool, error) {
	m.resolver.DeleteCaches(ctx, shortCode)
	deleted, err := m.store.DeleteByShortCode(ctx, shortCode)
	m.resolver.DeleteCaches(ctx, shortCode)
	if err != nil {
		return false, fmt.Errorf("delete short url: %w", err)
	}
	return deleted, nil
}
