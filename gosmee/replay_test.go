package gosmee

import (
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
)

func TestChooseDeliveries(t *testing.T) {
	type args struct {
		sinceTime  time.Time
		deliveries []*github.HookDelivery
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		deliveryCount int
		deliveryIDs   []int64
	}{
		{
			name:          "choose deliveries",
			deliveryCount: 2,
			deliveryIDs:   []int64{2, 3},
			args: args{
				sinceTime: time.Now(),
				deliveries: []*github.HookDelivery{
					{
						ID:          github.Int64(3),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(1 * time.Hour)},
					},
					{
						ID:          github.Int64(2),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(2 * time.Hour)},
					},
					{
						ID:          github.Int64(1),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(-1 * time.Hour)},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &replayOpts{sinceTime: tt.args.sinceTime}
			ret := r.chooseDeliveries(tt.args.deliveries)
			if len(ret) != tt.deliveryCount {
				t.Errorf("chooseDeliveries() = %v, want %v", len(ret), tt.deliveryCount)
			}
			for i, d := range ret {
				if *d.ID != tt.deliveryIDs[i] {
					t.Errorf("chooseDeliveries() = %v, want %v", *d.ID, tt.deliveryIDs[i])
				}
			}
		})
	}
}
