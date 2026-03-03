package playbackdomain

// TraversalOptions represents traversal semantics for a playback context.
type TraversalOptions struct {
	RepeatContext bool
	RepeatTrack   bool
	Shuffle       bool
}

// ResolveOptions applies partial option updates and enforces invariants.
// Invariant: RepeatTrack and RepeatContext are mutually exclusive; RepeatTrack wins.
func ResolveOptions(curr TraversalOptions, repeatingContext *bool, repeatingTrack *bool, shufflingContext *bool) TraversalOptions {
	next := curr
	if repeatingContext != nil {
		next.RepeatContext = *repeatingContext
	}
	if repeatingTrack != nil {
		next.RepeatTrack = *repeatingTrack
	}
	if shufflingContext != nil {
		next.Shuffle = *shufflingContext
	}
	if next.RepeatTrack {
		next.RepeatContext = false
	}
	return next
}

// NextRepeatTraversalOptions returns the next repeat mode in the cycle:
// off -> repeat-context -> repeat-track -> off.
func NextRepeatTraversalOptions(curr TraversalOptions) TraversalOptions {
	next := curr
	switch {
	case curr.RepeatTrack:
		next.RepeatContext = false
		next.RepeatTrack = false
	case curr.RepeatContext:
		next.RepeatContext = false
		next.RepeatTrack = true
	default:
		next.RepeatContext = true
		next.RepeatTrack = false
	}
	return next
}

