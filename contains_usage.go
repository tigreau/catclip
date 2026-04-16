package catclip

func containsMissingPatternError(args []string, containsIndex int) error {
	suggestionArgs, ok := suggestContainsSnippetOrdering(args, containsIndex)
	if !ok {
		return validationFailure{Reason: validationReasonRequiredValue, Flag: "--contains"}
	}
	if _, err := parseArgs(suggestionArgs); err != nil {
		return validationFailure{Reason: validationReasonRequiredValue, Flag: "--contains"}
	}
	return validationFailure{Reason: validationReasonRequiredValue, Flag: "--contains", Suggestion: formatResolvedStartupCommand(suggestionArgs)}
}

func suggestContainsSnippetOrdering(args []string, containsIndex int) ([]string, bool) {
	if containsIndex+2 >= len(args) {
		return nil, false
	}
	if args[containsIndex+1] != "--snippet" {
		return nil, false
	}
	regex := args[containsIndex+2]
	if isKnownScopeModifierToken(regex) || regex == "--" || regex == "--then" {
		return nil, false
	}

	suggestion := make([]string, 0, len(args))
	suggestion = append(suggestion, args[:containsIndex]...)
	suggestion = append(suggestion, "--snippet", regex)
	suggestion = append(suggestion, args[containsIndex+3:]...)
	return suggestion, true
}
