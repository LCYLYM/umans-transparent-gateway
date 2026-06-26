package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrConcurrencyQueueTimeout = errors.New("concurrency queue timeout")
	ErrConcurrencyWaitCanceled = errors.New("concurrency wait canceled")
)

type KeyLimiter struct {
	limit   int
	timeout time.Duration
	secret  []byte

	mu      sync.Mutex
	buckets map[string]*keyBucket
}

type keyBucket struct {
	active int
	queue  []chan struct{}
}

func NewKeyLimiter(limit int, timeout time.Duration) (*KeyLimiter, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &KeyLimiter{
		limit:   limit,
		timeout: timeout,
		secret:  secret,
		buckets: map[string]*keyBucket{},
	}, nil
}

func (l *KeyLimiter) Acquire(ctx context.Context, key string) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	id := l.keyID(key)
	ready := make(chan struct{})

	l.mu.Lock()
	b := l.buckets[id]
	if b == nil {
		b = &keyBucket{}
		l.buckets[id] = b
	}
	if b.active < l.limit && len(b.queue) == 0 {
		b.active++
		l.mu.Unlock()
		return l.releaseFunc(id), nil
	}
	b.queue = append(b.queue, ready)
	l.mu.Unlock()

	timer := time.NewTimer(l.timeout)
	defer timer.Stop()

	select {
	case <-ready:
		return l.releaseFunc(id), nil
	case <-timer.C:
		if l.removeWaiter(id, ready) {
			return nil, ErrConcurrencyQueueTimeout
		}
		<-ready
		return l.releaseFunc(id), nil
	case <-ctx.Done():
		if l.removeWaiter(id, ready) {
			return nil, ErrConcurrencyWaitCanceled
		}
		<-ready
		return l.releaseFunc(id), nil
	}
}

func (l *KeyLimiter) releaseFunc(id string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.release(id)
		})
	}
}

func (l *KeyLimiter) release(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[id]
	if b == nil {
		return
	}
	if len(b.queue) > 0 {
		next := b.queue[0]
		copy(b.queue, b.queue[1:])
		b.queue[len(b.queue)-1] = nil
		b.queue = b.queue[:len(b.queue)-1]
		close(next)
		return
	}
	if b.active > 0 {
		b.active--
	}
	if b.active == 0 && len(b.queue) == 0 {
		delete(l.buckets, id)
	}
}

func (l *KeyLimiter) removeWaiter(id string, ready chan struct{}) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[id]
	if b == nil {
		return false
	}
	for i, ch := range b.queue {
		if ch != ready {
			continue
		}
		copy(b.queue[i:], b.queue[i+1:])
		b.queue[len(b.queue)-1] = nil
		b.queue = b.queue[:len(b.queue)-1]
		if b.active == 0 && len(b.queue) == 0 {
			delete(l.buckets, id)
		}
		return true
	}
	return false
}

func (l *KeyLimiter) keyID(key string) string {
	mac := hmac.New(sha256.New, l.secret)
	_, _ = mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}
