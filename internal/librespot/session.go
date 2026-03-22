package librespot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/apresolve"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
	"github.com/elxgy/go-librespot/session"
)

const defaultCallbackPort = 8080

type SessionOptions struct {
	ConfigDir    string
	CallbackPort int
	DeviceType   string
	ClientToken  string
}

func EnsureConfigDir(configDir string) error {
	if configDir == "" {
		return fmt.Errorf("config dir is empty")
	}
	return os.MkdirAll(configDir, 0o700)
}

func parseDeviceType(val string) (devicespb.DeviceType, error) {
	if val == "" {
		val = "computer"
	}
	key := strings.ToUpper(val)
	enum, ok := devicespb.DeviceType_value[key]
	if !ok {
		return 0, fmt.Errorf("invalid device type: %s", val)
	}
	return devicespb.DeviceType(enum), nil
}

func ensureDeviceID(state *golibrespot.AppState) error {
	if state.DeviceId != "" {
		return nil
	}
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("generate device id: %w", err)
	}
	state.DeviceId = hex.EncodeToString(b)
	return state.Write()
}

func NewSession(ctx context.Context, log golibrespot.Logger, opts SessionOptions) (*session.Session, *golibrespot.AppState, error) {
	if opts.ConfigDir == "" {
		return nil, nil, fmt.Errorf("config dir required")
	}
	if err := EnsureConfigDir(opts.ConfigDir); err != nil {
		return nil, nil, fmt.Errorf("ensure config dir: %w", err)
	}

	deviceType, err := parseDeviceType(opts.DeviceType)
	if err != nil {
		return nil, nil, err
	}

	appState := &golibrespot.AppState{}
	appState.SetLogger(log)
	if err := appState.Read(opts.ConfigDir); err != nil {
		return nil, nil, fmt.Errorf("read app state: %w", err)
	}

	if err := ensureDeviceID(appState); err != nil {
		return nil, nil, fmt.Errorf("ensure device id: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resolver := apresolve.NewApResolver(log, client)

	callbackPort := opts.CallbackPort
	if callbackPort <= 0 {
		callbackPort = defaultCallbackPort
	}

	var creds any
	if len(appState.Credentials.Data) > 0 {
		creds = session.StoredCredentials{
			Username: appState.Credentials.Username,
			Data:     appState.Credentials.Data,
		}
	} else {
		creds = session.InteractiveCredentials{CallbackPort: callbackPort}
	}

	sessOpts := &session.Options{
		Log:         log,
		DeviceType:  deviceType,
		DeviceId:    appState.DeviceId,
		Credentials: creds,
		ClientToken: opts.ClientToken,
		Resolver:    resolver,
		Client:      client,
		AppState:    appState,
	}

	sess, err := session.NewSessionFromOptions(ctx, sessOpts)
	if err != nil {
		return nil, nil, err
	}

	_, isInteractive := creds.(session.InteractiveCredentials)
	if isInteractive {
		appState.Credentials.Username = sess.Username()
		appState.Credentials.Data = sess.StoredCredentials()
		if err := appState.Write(); err != nil {
			sess.Close()
			return nil, nil, fmt.Errorf("persist credentials: %w", err)
		}
		log.WithField("username", golibrespot.ObfuscateUsername(sess.Username())).Debugf("stored credentials")
	}

	return sess, appState, nil
}
