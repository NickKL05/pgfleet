-- pgfleet demo: introduce deliberate drift into 3 of the 250 tenants after they
-- have been migrated. Run this once `pgfleet migrate up` has completed so that
-- `pgfleet drift verify` flags exactly tenant_087, tenant_142, and tenant_199.

-- tenant_087: a missing index (someone dropped it by hand).
drop index if exists tenant_087.users_created_at_idx;

-- tenant_142: a modified column type (text widened to a bounded varchar).
alter table tenant_142.users
    alter column display_name type varchar(100);

-- tenant_199: an extra, rogue table that does not belong in the canonical shape.
create table if not exists tenant_199.audit_log (
    id      bigint generated always as identity primary key,
    note    text not null,
    at      timestamptz not null default now()
);
