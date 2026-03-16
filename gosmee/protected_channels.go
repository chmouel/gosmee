package gosmee

import (
	"encoding/json"
	"fmt"
	"os"
)

type protectedChannelsFile struct {
	Channels map[string]protectedChannelConfig `json:"channels"`
}

type protectedChannelConfig struct {
	AllowedPublicKeys []string `json:"allowed_public_keys"`
}

type ProtectedChannels struct {
	channels map[string]map[string]struct{}
}

func LoadProtectedChannels(path string) (*ProtectedChannels, error) {
	if path == "" {
		return &ProtectedChannels{
			channels: make(map[string]map[string]struct{}),
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg protectedChannelsFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal encrypted channels file: %w", err)
	}
	if len(cfg.Channels) == 0 {
		return nil, fmt.Errorf("encrypted channels file must define at least one channel")
	}

	protected := &ProtectedChannels{
		channels: make(map[string]map[string]struct{}, len(cfg.Channels)),
	}

	for channel, channelCfg := range cfg.Channels {
		if channel == "" {
			return nil, fmt.Errorf("encrypted channels file contains an empty channel id")
		}
		if !isValidChannelID(channel) {
			return nil, fmt.Errorf("encrypted channel %q must match %q", channel, channelIDPattern)
		}
		if len(channelCfg.AllowedPublicKeys) == 0 {
			return nil, fmt.Errorf("encrypted channel %q must define at least one allowed public key", channel)
		}

		allowed := make(map[string]struct{}, len(channelCfg.AllowedPublicKeys))
		for _, encodedKey := range channelCfg.AllowedPublicKeys {
			publicKey, err := ParsePublicKey(encodedKey)
			if err != nil {
				return nil, fmt.Errorf("invalid public key for channel %q: %w", channel, err)
			}
			allowed[EncodePublicKey(publicKey)] = struct{}{}
		}

		protected.channels[channel] = allowed
	}

	return protected, nil
}

func (p *ProtectedChannels) Has(channel string) bool {
	if p == nil {
		return false
	}

	_, ok := p.channels[channel]
	return ok
}

func (p *ProtectedChannels) IsAllowed(channel string, publicKey *[32]byte) bool {
	if p == nil || publicKey == nil {
		return false
	}

	allowedKeys, ok := p.channels[channel]
	if !ok {
		return false
	}

	_, ok = allowedKeys[EncodePublicKey(publicKey)]
	return ok
}
