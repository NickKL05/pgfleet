-- pgfleet demo seed: a control table, 250 empty tenant schemas, and a canonical
-- tenant_template used as the drift reference.
--
-- After loading this file, `pgfleet migrate up` creates the users table and its
-- index in all 250 tenants. The template already carries the canonical shape so
-- `pgfleet drift verify` (milestones M3 to M5) can compare against it.

create schema if not exists control;

create table if not exists control.tenants (
    schema_name text primary key,
    active      boolean not null default true
);

-- Build the 250 tenant schemas and register them for query-mode discovery.
do $$
declare
    i int;
    s text;
begin
    for i in 1..250 loop
        s := format('tenant_%s', lpad(i::text, 3, '0'));
        execute format('create schema if not exists %I', s);
        insert into control.tenants (schema_name, active)
        values (s, true)
        on conflict (schema_name) do nothing;
    end loop;
end $$;

-- The canonical reference schema. This mirrors the end state of the migrations
-- in ../migrations and is excluded from discovery via pgfleet.yaml.
create schema if not exists tenant_template;

create table if not exists tenant_template.users (
    id           bigint generated always as identity primary key,
    email        text not null unique,
    display_name text not null,
    created_at   timestamptz not null default now()
);

create index if not exists users_created_at_idx
    on tenant_template.users (created_at);
