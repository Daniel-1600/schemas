package validation

// Severity classification — ported from build/lib/consistency-policy.js.
//
// These functions determine whether a particular category of issue is
// reported as an error, a warning, or suppressed entirely, based on
// the audit options in effect.

// classifyStyleIssue determines the severity for naming convention issues
// (Rules 3, 4, 6, 7, 8, 9, 10, 19).
func classifyStyleIssue(opts AuditOptions) *Severity {
	if opts.Strict {
		s := SeverityBlocking
		return &s
	}
	if opts.StyleDebt {
		s := SeverityAdvisory
		return &s
	}
	return nil // suppressed
}

// classifyDesignIssue determines the severity for API design pattern issues
// (Rules 23, 24, 25, 26, 30, 31, 36, 37).
func classifyDesignIssue(opts AuditOptions) Severity {
	if opts.Strict {
		return SeverityBlocking
	}
	return SeverityAdvisory
}

// classifyContractIssue determines the severity for published API contract
// issues (Rules 28, 29).
func classifyContractIssue(opts AuditOptions) *Severity {
	if opts.Strict {
		s := SeverityBlocking
		return &s
	}
	s := SeverityAdvisory
	return &s
}
