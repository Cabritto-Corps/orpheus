package playbackdomain

type TraversalOptions struct {
	RepeatContext bool
	RepeatTrack   bool
	Shuffle       bool
}

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

