-- 0001 create users table (up)
-- Runs inside the tenant schema with search_path already set to the tenant.

create table users (
    id           bigint generated always as identity primary key,
    email        text not null unique,
    display_name text not null,
    created_at   timestamptz not null default now()
);
