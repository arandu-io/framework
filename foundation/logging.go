// The root logger of a configured Application, built from the typed logging
// section rather than written by the application.

package foundation

import (
	"fmt"
	"log/slog"

	"github.com/arandu-io/framework/foundation/bootstrap"
	hlog "github.com/arandu-io/hesape/log"
)

// stdoutChannel is the one channel the root logger writes to: the process
// standard output, where a container's collector reads.
const stdoutChannel = "stdout"

// levelNames are the names a channel's level is written in. A channel takes
// the level by name, and the typed section holds it parsed, so it is written
// back under the name it was parsed from.
var levelNames = []string{"debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"}

// rootLogger is the logger the logging section describes: the standard output,
// at LOG_LEVEL, in LOG_FORMAT -- text in development and JSON everywhere else
// when the format is left empty -- each line carrying the environment as its
// channel.
func rootLogger(s bootstrap.Settings) (*slog.Logger, error) {
	level, err := levelName(s.Logging.Level)
	if err != nil {
		return nil, err
	}
	logger, err := hlog.FromConfig(hlog.Config{
		Default: stdoutChannel,
		Env:     string(s.App.Env),
		Channels: map[string]hlog.ChannelConfig{
			stdoutChannel: {Driver: stdoutChannel, Level: level, Format: s.Logging.Format},
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("arandu: building the logger from the logging settings: %w", err)
	}
	return logger, nil
}

// levelName answers the name a level is configured by, and refuses a level
// none of the eight names spells rather than writing more than was asked for.
func levelName(l slog.Level) (string, error) {
	for _, name := range levelNames {
		if parsed, err := hlog.ParseLevel(name); err == nil && parsed == l {
			return name, nil
		}
	}
	return "", fmt.Errorf("arandu: the log level %s is none of %v", l, levelNames)
}
