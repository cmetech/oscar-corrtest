# Scenario authoring and workbench

The Scenarios page is a three-pane engineering workbench:

1. **Scenario catalog** lists all eight immutable built-ins and imported custom
   documents.
2. **Source** shows canonical strict YAML. Select **Edit a copy** to open an
   unsaved editable draft of a built-in. The draft is not written to SQLite.
3. **Compiled contract** shows both P01 and N01 rules, alert stimuli, reserved
   labels, assertions, observation windows, OSCAR inspection filters, and the
   mutation budget.

Preview and validation are target-free: they do not resolve an API key, contact
OSCAR, create a rule, inject an alert, or write a run. **Save custom scenario**
stores the exact validated source and SHA-256 digest in SQLite. Editing a saved
scenario and choosing **Save as new version** preserves the old immutable
version and creates a new one. **Delete custom scenario** removes an unused
version after confirmation; a version referenced by historical runs cannot be
deleted until those runs are removed.

## Required document shape

```yaml
apiVersion: corrtest.oscar/v1alpha1
kind: CorrelationScenario
name: flood-lab-custom
suite: custom
pattern: flood
maxDuration: 1m30s
cases:
  - name: emits-at-threshold
    code: P01
    polarity: positive
    role: interface_down
    repeat: 5
    window: 30s
    groupBy: [site]
    labels: {site: lab-a}
    assertions:
      - {kind: synthetic-alert-count, equals: 1}
  - name: remains-below-threshold
    code: N01
    polarity: negative
    role: interface_down
    repeat: 4
    window: 30s
    groupBy: [site]
    labels: {site: lab-a}
    assertions:
      - {kind: synthetic-alert-count, equals: 0}
```

P01 is the expected triggering case. N01 is the nearby negative control. Never
treat absence of a synthetic alert by itself as proof: the harness requires the
declared assertions, a complete observation window, audit/history evidence,
and cleanup evidence.

## Names and manual OSCAR inspection

Physical alert names follow
`CORRTEST_<PATTERN_CODE>_<CASE_CODE>_<ROLE>_<RUN_SHORT>`. Every sent and expected
synthetic alert includes `category=corrtest_<pattern>`, `oscar_test_run_id`,
`oscar_test_pattern`, `oscar_test_case_code`, polarity, class, role, scenario,
suite, and temporary rule name. Use the compiled contract's exact filters when
manually inspecting a run in OSCAR.

The eight supported patterns are `co_occurrence`, `flood`, `sequence`,
`persistence`, `absence`, `parent_child`, `cross_source`, and `threshold`.
Unknown fields, aliases, multiple YAML documents, unsafe labels, reserved-label
overrides, invalid durations, and documents over 1 MiB are rejected.

CLI equivalents:

```sh
oscar-corrtest scenario validate ./scenario.yaml
oscar-corrtest scenario import ./scenario.yaml
oscar-corrtest scenario list
```
