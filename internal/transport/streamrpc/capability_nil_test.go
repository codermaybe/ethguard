package streamrpc

import (
	"context"
	"testing"

	"github.com/codermaybe/ethguard/internal/transport"
)

func TestFailedSubscriptionCapabilitiesReturnNilInterface(t *testing.T) {
	tr := newPendingTestTransport(t)
	for _, bounded := range []bool{false, true} {
		var sub transport.Subscription
		var err error
		if bounded {
			sub, err = tr.SubscribeBounded(context.Background(), transport.NotificationPolicy{}, "")
		} else {
			sub, err = tr.SubscribeCapability(context.Background(), "")
		}
		if err == nil || sub != nil {
			t.Fatalf("bounded=%v sub=%#v err=%v", bounded, sub, err)
		}
	}
}
