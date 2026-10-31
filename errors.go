package highlights

import "errors"

// Errors identify the kinds of failure; test them with errors.Is. The
// messages match those of the TypeScript package.
var (
	// ErrUnknownLanguage reports a language name without a lexer.
	ErrUnknownLanguage = errors.New("unknown language")
	// ErrInvalidTheme reports a missing or malformed theme or theme set.
	ErrInvalidTheme = errors.New("invalid theme")
	// ErrInvalidArgument reports an argument of the wrong shape, such as a
	// negative edit position or a reversed render range.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrOutOfRange reports a line or character outside the document, or
	// edits that overlap.
	ErrOutOfRange = errors.New("out of range")
	// ErrStreamEnded is returned after a stream has ended or closed.
	ErrStreamEnded = errors.New("stream has ended")
	// ErrClosed is returned after a live tokenizer has closed.
	ErrClosed = errors.New("tokenizer is disposed")
	// ErrReentrantUpdate is returned by ApplyEdits or Reset when called from
	// OnDeferTokenize during an update.
	ErrReentrantUpdate = errors.New("applyEdits and reset cannot be called from onDeferTokenize during an update; use pause, flush, or the reads instead")
)

// Error is an error of one of the kinds above with a specific message.
type Error struct {
	Kind    error
	Message string
}

func (e *Error) Error() string { return e.Message }

// Unwrap returns the error's kind.
func (e *Error) Unwrap() error { return e.Kind }

func newError(kind error, message string) error {
	return &Error{Kind: kind, Message: message}
}
