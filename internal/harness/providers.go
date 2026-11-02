package harness

import (
	"errors"
	"fmt"
)

var ErrProviderDisabled = errors.New("harness: Provider is disabled; enable it or explicitly select another model")

func (config Config) ProviderEnabled(model ModelConfig) bool {
	return model.Provider == "echo" || model.Key == "" || config.Providers[model.Key]
}

func (service *Service) providerConfig() (Config, error) {
	if service.Refresh != nil {
		return service.Refresh()
	}
	if service.liveConfig != nil {
		return service.liveConfig(), nil
	}
	return service.currentConfig(), nil
}

func (service *ControlService) validateProvider(raw []byte) error {
	var args struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeControlJSON(raw, &args); err != nil {
		return err
	}
	if service.core.Keys == nil {
		return ErrKeyNotFound
	}
	profile, err := service.core.Keys.Find(args.ID)
	if err != nil {
		return err
	}
	if IsChannelKeyProtocol(profile.Protocol) {
		return errors.New("harness: channel credentials use channel switches")
	}
	return nil
}

func (config Config) requireProvider(model ModelConfig) error {
	if !config.ProviderEnabled(model) {
		return fmt.Errorf("%w: %s", ErrProviderDisabled, model.Key)
	}
	return nil
}
