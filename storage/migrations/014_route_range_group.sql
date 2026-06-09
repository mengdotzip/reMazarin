-- Port-range routes created in the admin UI are expanded into one row per port
-- (each port needs its own listener). Rows that originate from the same range
-- share a generated range_group id so the admin UI can collapse them into a
-- single logical route and cascade edits/deletes across the whole range.
-- Single (non-range) routes keep the empty default.
ALTER TABLE proxy_routes ADD COLUMN range_group TEXT DEFAULT '';
