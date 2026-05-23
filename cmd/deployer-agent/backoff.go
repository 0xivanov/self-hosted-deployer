package main

import "time"

const (
	initialHeartbeatBackoff = 5 * time.Second
	maxHeartbeatBackoff     = time.Minute
)

type heartbeatBackoff struct {
	initial time.Duration
	max     time.Duration
	next    time.Duration
}

func newBackoff(initial time.Duration, max time.Duration) *heartbeatBackoff {
	return &heartbeatBackoff{initial: initial, max: max}
}

func (b *heartbeatBackoff) Next() time.Duration {
	if b.next <= 0 {
		b.next = b.initial
	}
	delay := b.next
	b.next *= 2
	if b.next > b.max {
		b.next = b.max
	}
	return delay
}

func (b *heartbeatBackoff) Reset() {
	b.next = 0
}
