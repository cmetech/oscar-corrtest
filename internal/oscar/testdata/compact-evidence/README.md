# Compact evidence wire fixtures

`projector-response.json` is copied byte-for-byte from OSCAR's
`docs/qualification/20261020/compact-evidence-api/projection/gateway-hardening/projector-response.json`.
`mysql-accepted.json` is copied from the same qualification's `mysql/attempt-06/accepted.json`.
They represent local projector and disposable-MySQL evidence, not live UAT acceptance.

`unicode-int64-python.json` modifies the first fixture's display strings and partition/queue integers
solely to exercise wire encoding. Python `json.dumps(sort_keys=True, separators=(',', ':'),
ensure_ascii=False, allow_nan=False)` independently computed its SHA256 after excluding
`evidence_sha256`. Strings include non-ASCII, HTML characters, U+2028/U+2029 and emoji;
integers include 9223372036854775807. This synthetic codec fixture is not a retained database proof.
