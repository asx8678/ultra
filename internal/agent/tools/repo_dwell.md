Expand the current session's repository focus and return only newly relevant graph context.

Call repo_focus successfully before this tool. Dwell preserves the seed and graph generation established by focus while revealing the next bounded region without repeating already returned nodes. If the repository changed, the saved focus is refreshed and the result is marked degraded instead of using stale node IDs. Do not run focus and dwell concurrently or place them in Promise.all.
