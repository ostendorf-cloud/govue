-- The example's schema. One file runs in one transaction with its
-- bookkeeping row, so "create index concurrently" cannot be used here.
create table notes (
    id         bigint      generated always as identity primary key,
    body       text        not null check (length(trim(body)) > 0),
    created_at timestamptz not null default now()
);
