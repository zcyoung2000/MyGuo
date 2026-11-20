# Watching history

History stays in `~/Library/Application Support/myguo/userdata.db` with SQLite WAL.
No production history is seeded or rewritten by this change.

## Classification

Both native and embedded history views require a known final episode and at least
95% of that episode's actual duration to label a series completed. Missing
metadata does not imply completion. An intermediate episode at 100% is unfinished.

OpenHongguo's `model.ContentShortSeries` is 1 (真人短剧), not a novel. The old JSON
migration, batch writer and query excluded that value, while the cloud merge even
queued its deletion. Those exclusions and automatic deletion are removed.

Cloud history's episode count is used when provided. A duration is reused only
for the same episode. For a final episode with missing duration, the metadata
worker checks the episode details. If duration remains unavailable, completion
stays unconfirmed.

## Existing bidirectional synchronization

- `SaveProgress` persists authentic playback progress, invalidates the history
  cache and queues an upload under the current identity. Background upload uses
  `read_progress/upload` as the authoritative playback clock and acknowledges
  only successful writes; failed writes remain queued for retry on subsequent
  saves or refreshes. `read_history/update` is retained for delete compatibility.
- `GetHistory` returns local rows promptly and revalidates in the background.
  `RefreshHistory` uploads pending local progress independently, reads every
  cloud page, persists progress, then returns the current SQLite snapshot.
  Native history loads local then refreshes; embedded history uses the same IPC
  methods.
- Cursor progress and explicit `has_more` determine page completion. A repeated
  or stalled cursor and any later-page error produce a partial snapshot; partial
  snapshots merge fetched progress but never delete local rows. Only a complete,
  settled snapshot with no pending local mutations may remove rows absent from
  the cloud.
- The cloud progress list does not provide poster art. Missing titles, covers,
  episode counts and the final episode duration are stored in a durable,
  account-scoped metadata queue. The worker requests batches of 10, falls back
  to individual series details for missing or rejected batches, and persists
  each resolved record without changing its playback clock. Failures remain
  queued with exponential retry timing, including across app restarts. Content
  explicitly reported as unavailable is removed from the retry queue and kept
  in an internal suppression record, with a recheck after seven days. Its
  actual playback progress is retained. Metadata maintenance and unavailable
  content do not turn a successful history read into a user-facing error.
- The UI displays partial results with a retryable sync error for real read or
  upload failures. Metadata changes
  invalidate the history view; absent or failed cover art is labelled so a
  broken poster is visible rather than silently treated as a valid image.
- Legacy history upload and delete tasks with no account scope cannot safely
  be sent. Startup and cloud refresh retire them automatically in a transaction,
  without assigning them to the active account or showing a warning. Visible
  history covered by those uploads is retained locally and excluded from remote
  pruning, without remaining in the retry queue. Fresh playback uses the normal
  scoped upload and ends local-only retention. Account switches and signout
  clear retained visible history as usual; tasks owned by known accounts are
  untouched.
- Generation and identity checks discard stale reads. A recent local mutation
  prevents a lagging cloud snapshot from replacing local rows. Complete, settled
  snapshots can reflect deletions made elsewhere. HTTP failures, nonzero response
  codes and missing history payloads cannot become an authoritative empty list.
- Single-record deletion and clear-all are removed from native and embedded UI,
  application callbacks, library service and exported IPC. Store deletion helpers
  and legacy pending-operation handling remain internal compatibility machinery.

`internal/hgapi` owns requests for cloud history and series metadata.
`internal/library` owns synchronization, retries, and account scope.

Regression coverage uses temporary SQLite databases and stub HTTP and metadata
providers. It verifies pagination beyond ten pages, partial-page persistence,
cursor failure safety, metadata queue backoff and enrichment, artwork
normalization, direct progress upload and acknowledgment, and partial-result UI.
Authenticated live-cloud behavior is not asserted by these tests.
