package linkoerr

import (
	"errors"
	"log/slog"
)

type errWithAttrr struct {
	error
	attrs []slog.Attr
}

type attrError interface {
	Attrs() []slog.Attr
}

func (e *errWithAttrr) Unwrap() error {
	return e.error
}

func (e *errWithAttrr) Attrs() []slog.Attr {
	return e.attrs
}

func WithAttr(err error, args ...any) error {
	return &errWithAttrr{
		error: err,
		attrs: argsToAttr(args),
	}
}

// Attrs recursively extracts all logging attributes from an error chain. In the
// case of a duplication of keys, the outermost c=value takes the precedence.
func Attrs(err error) []slog.Attr {
	var attrs []slog.Attr
	for err != nil {
		if ae, ok := err.(attrError); ok {
			attrs = append(attrs, ae.Attrs()...)
		}
		err = errors.Unwrap(err)
	}
	return attrs
}

// argsToAttr turns a list of type or untyped args to a lisce of [slog.Attr]
// args[i] is treated as a key if it is a string or an [slog.Attr]; otherwise, it
// is treated as avalue with key "!BADKEY".
func argsToAttr(args []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(args))
	for i := 0; i < len(args); {
		switch key := args[i].(type) {
		case slog.Attr:
			attrs = append(attrs, key)
			i++
		case string:
			if i+1 >= len(args) {
				attrs = append(attrs, slog.String("!BADKEY", key))
				i++
			} else {
				attrs = append(attrs, slog.Any(key, args[i+1]))
				i += 2
			}
		default:
			attrs = append(attrs, slog.Any("!BADKEY", args[i]))
			i++
		}
	}
	return attrs
}
