package pool

import (
	"fmt"
	"net/url"
	"strings"
)

func normalizeEndpointID(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("pool: parse url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("pool: url must include scheme and host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

// Join registers an endpoint if it is not already present. Concurrent calls
// for the same normalized endpoint always return the same Source.
func (p *Pool) Join(name, rawURL string) (*Source, error) {
	id, err := normalizeEndpointID(rawURL)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	if existing := p.byID[id]; existing != nil {
		return existing, nil
	}

	src := &Source{
		name:   name,
		url:    rawURL,
		id:     id,
		dialer: p.dialer,
		state:  SourceDown,
	}
	p.sources = append(p.sources, src)
	p.byID[id] = src
	return src, nil
}
