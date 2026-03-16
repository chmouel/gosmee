package gosmee

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLoadProtectedChannels(t *testing.T) {
	t.Run("empty path disables protected channels", func(t *testing.T) {
		protectedChannels, err := LoadProtectedChannels("")
		assert.NilError(t, err)
		assert.Assert(t, !protectedChannels.Has("test-channel"))
	})

	t.Run("valid config", func(t *testing.T) {
		publicKey, _, err := GenerateKeyPair()
		assert.NilError(t, err)

		cfg := protectedChannelsFile{
			Channels: map[string]protectedChannelConfig{
				"test-channel": {
					AllowedPublicKeys: []string{EncodePublicKey(publicKey)},
				},
			},
		}

		data, err := json.Marshal(cfg)
		assert.NilError(t, err)

		path := filepath.Join(t.TempDir(), "channels.json")
		assert.NilError(t, os.WriteFile(path, data, 0o600))

		protectedChannels, err := LoadProtectedChannels(path)
		assert.NilError(t, err)
		assert.Assert(t, protectedChannels.Has("test-channel"))
		assert.Assert(t, protectedChannels.IsAllowed("test-channel", publicKey))
	})

	t.Run("invalid public key", func(t *testing.T) {
		cfg := protectedChannelsFile{
			Channels: map[string]protectedChannelConfig{
				"test-channel": {
					AllowedPublicKeys: []string{"not-a-key"},
				},
			},
		}

		data, err := json.Marshal(cfg)
		assert.NilError(t, err)

		path := filepath.Join(t.TempDir(), "channels.json")
		assert.NilError(t, os.WriteFile(path, data, 0o600))

		_, err = LoadProtectedChannels(path)
		assert.Assert(t, err != nil)
	})

	t.Run("channel id must match server routes", func(t *testing.T) {
		publicKey, _, err := GenerateKeyPair()
		assert.NilError(t, err)

		cfg := protectedChannelsFile{
			Channels: map[string]protectedChannelConfig{
				"abc": {
					AllowedPublicKeys: []string{EncodePublicKey(publicKey)},
				},
			},
		}

		data, err := json.Marshal(cfg)
		assert.NilError(t, err)

		path := filepath.Join(t.TempDir(), "channels.json")
		assert.NilError(t, os.WriteFile(path, data, 0o600))

		_, err = LoadProtectedChannels(path)
		assert.ErrorContains(t, err, `must match`)
	})
}
