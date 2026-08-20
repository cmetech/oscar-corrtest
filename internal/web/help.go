package web

// HelpSection is escaped, static operator guidance rendered in the drawer and Reference page.
type HelpSection struct {
	Heading    string
	Paragraphs []string
	Bullets    []string
	Code       string
}

// HelpLink connects guidance to a live console surface.
type HelpLink struct {
	Label string
	Href  string
}

// HelpTopic is one page or correlation-pattern reference topic.
type HelpTopic struct {
	ID       string
	Title    string
	Summary  string
	Sections []HelpSection
	Links    []HelpLink
}

// HelpCatalog is an immutable, stable-order help collection.
type HelpCatalog struct {
	topics []HelpTopic
	index  map[string]int
}

func (c HelpCatalog) Topic(id string) (HelpTopic, bool) {
	index, ok := c.index[id]
	if !ok {
		return HelpTopic{}, false
	}
	return cloneTopic(c.topics[index]), true
}

func (c HelpCatalog) All() []HelpTopic {
	result := make([]HelpTopic, len(c.topics))
	for index, topic := range c.topics {
		result[index] = cloneTopic(topic)
	}
	return result
}

func defaultHelpCatalog() HelpCatalog {
	pages := []struct{ id, title, summary, workflow, effect, evidence, cli string }{
		{"dashboard", "Dashboard reference", "Read current harness readiness and the fastest route into a correlation qualification run.", "Configure an OSCAR target, inspect a scenario, then start a run and follow its durable status.", "Reading this page does not contact or mutate OSCAR.", "Readiness comes from the local SQLite migration and storage checks.", "oscar-corrtest runs"},
		{"targets", "Targets reference", "Store endpoint metadata and credential references without storing resolved credential values.", "Add the OSCAR external API base URL. Leave credential source empty to use the global OSCAR_API_KEY, or select an advanced per-target reference.", "Target creation is local only; connection and rule validation happen during doctor or run preflight.", "Target metadata is stored in SQLite. API keys are never included.", "oscar-corrtest target add --name lab --url https://oscar.example/ext/mw"},
		{"run-test", "Run test reference", "Launch a positive and negative black-box test for one built-in correlation pattern.", "Choose a target, pattern, and the target's known pipeline mode. Phase B is required for synthetic-parent assertions.", "The harness validates and creates temporary rules, injects reserved-label alerts, observes audit/history evidence, resolves alerts, and removes owned rules.", "A PASS requires declared assertions plus complete cleanup evidence; absence alone is never proof.", "oscar-corrtest run builtin:flood --target <target-id> --pipeline-mode phase_b_dispatch"},
		{"scenarios", "Scenario workbench reference", "Inspect canonical built-ins or author strict custom YAML before any live mutation.", "Select a built-in, review P01 and N01, clone it, edit the imported source, validate, and run only after the compiled contract matches intent.", "Preview and validation are target-free. A live run performs the same guarded OSCAR lifecycle as a built-in.", "The source digest, compiled plan, assertion rows, raw observations, and cleanup state become durable evidence.", "oscar-corrtest scenario validate scenario.yaml"},
		{"runs", "Runs reference", "Filter durable run history by status, verdict, cleanup, and correlation pattern.", "Filter a run, open its timeline, then export the verified bundle when the run is terminal.", "Filtering and reading runs are local-only operations.", "Each run preserves normalized case/assertion/attempt rows and immutable evidence artifacts.", "oscar-corrtest runs --status COMPLETED --verdict PASS --pattern flood"},
		{"run-detail", "Run detail reference", "Inspect the complete lifecycle, assertion verdict, cleanup state, and manual OSCAR filter contract for one run.", "Confirm terminal state, read failures or inconclusive evidence, inspect exact labels in OSCAR, and export the evidence bundle.", "Cancel requests stop active injection and enter bounded cleanup. Delete is allowed only after clean/not-required cleanup.", "The timeline, report, compiled plan, artifact digests, and cleanup ownership are the proof chain.", "oscar-corrtest export --run <run-id> --output evidence.zip"},
		{"operations", "Operations reference", "Manage the global OSCAR key, effective user paths, background service, and redacted application logs.", "Save or clear the write-only key, inspect service state, and use live logs for operational diagnosis.", "A replaced key is used by newly constructed OSCAR clients. Service stop/restart controls only this user's CorrTest service.", "Operational logs are redacted diagnostics and are not correlation verdict evidence.", "oscar-corrtest service status; oscar-corrtest service logs"},
		{"reference", "Reference guide", "Use this page as the complete technical map of CorrTest behavior and correlation patterns.", "Start with the page topic matching your task, then follow pattern and naming guidance before running against OSCAR.", "This page is read-only and performs no OSCAR or local mutation.", "Reference copy explains evidence contracts; actual verdict proof remains attached to each run.", "oscar-corrtest help"},
	}
	var topics []HelpTopic
	for _, page := range pages {
		topics = append(topics, HelpTopic{ID: page.id, Title: page.title, Summary: page.summary, Sections: []HelpSection{
			{Heading: "Purpose", Paragraphs: []string{page.summary}},
			{Heading: "Workflow", Paragraphs: []string{page.workflow}},
			{Heading: "OSCAR effect", Paragraphs: []string{page.effect}},
			{Heading: "Evidence", Paragraphs: []string{page.evidence}},
			{Heading: "CLI equivalent", Code: page.cli},
		}})
	}
	patterns := []struct{ id, title, detail string }{
		{"pattern-co-occurrence", "co_occurrence", "Required alert roles occur in the same grouping window."},
		{"pattern-flood", "flood", "A minimum number of distinct alert fingerprints arrives inside the window."},
		{"pattern-sequence", "sequence", "Required alert roles arrive in the configured order."},
		{"pattern-persistence", "persistence", "A firing alert remains unresolved for the required duration."},
		{"pattern-absence", "absence", "An expected heartbeat does not reappear before the absence deadline."},
		{"pattern-parent-child", "parent_child", "A child links to an active parent and may be suppressed for a configured notifier."},
		{"pattern-cross-source", "cross_source", "Equivalent events from the required distinct OSCAR sources correlate."},
		{"pattern-threshold", "threshold", "Distinct grouping values meet a configured cardinality threshold."},
	}
	for _, pattern := range patterns {
		topics = append(topics, HelpTopic{ID: pattern.id, Title: pattern.title + " pattern", Summary: pattern.detail, Sections: []HelpSection{
			{Heading: "Purpose", Paragraphs: []string{pattern.detail}},
			{Heading: "Workflow", Paragraphs: []string{"P01 is the positive case expected to produce the declared outcome. N01 is the negative control expected not to produce it."}},
			{Heading: "OSCAR effect", Paragraphs: []string{"CorrTest creates a run-owned temporary rule and injects alerts with distinct identities where the pattern requires them."}},
			{Heading: "Evidence", Paragraphs: []string{"Audit/history observations, server fingerprints, assertion rows, and clean deletion must agree before PASS."}},
			{Heading: "CLI equivalent", Code: "oscar-corrtest run builtin:" + pattern.title + " --target <target-id> --pipeline-mode phase_b_dispatch"},
		}})
	}
	topics = append(topics, HelpTopic{ID: "naming-labels", Title: "Naming and label contract", Summary: "Filter CorrTest activity manually in OSCAR without guessing.", Sections: []HelpSection{
		{Heading: "Purpose", Paragraphs: []string{"Alert names use CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>. category uses corrtest_<pattern>."}},
		{Heading: "Workflow", Bullets: []string{"Filter oscar_test_run_id for one run.", "Filter oscar_test_pattern and oscar_test_case for pattern/polarity.", "Use alertname and category for readable manual inspection."}},
		{Heading: "OSCAR effect", Paragraphs: []string{"Reserved labels survive injection and are carried onto synthetic parents by the temporary rule emit contract."}},
		{Heading: "Evidence", Paragraphs: []string{"Server read-back proves labels and fingerprints rather than trusting the sent payload."}},
		{Heading: "CLI equivalent", Code: "oscar-corrtest runs --pattern <pattern>"},
	}})
	index := make(map[string]int, len(topics))
	for position, topic := range topics {
		index[topic.ID] = position
	}
	return HelpCatalog{topics: topics, index: index}
}

func cloneTopic(topic HelpTopic) HelpTopic {
	result := topic
	result.Links = append([]HelpLink(nil), topic.Links...)
	result.Sections = make([]HelpSection, len(topic.Sections))
	for index, section := range topic.Sections {
		result.Sections[index] = section
		result.Sections[index].Paragraphs = append([]string(nil), section.Paragraphs...)
		result.Sections[index].Bullets = append([]string(nil), section.Bullets...)
	}
	return result
}
