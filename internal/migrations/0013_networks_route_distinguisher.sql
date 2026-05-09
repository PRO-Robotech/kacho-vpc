-- +goose Up
--
-- Add route_distinguisher column to networks (BGP/MPLS-VPN sense, RFC 4364).
--
-- Optional internal-only field used by future kacho-vpc-implement integrations
-- (controller may consult it when synthesising SRv6 SID-encoded VPN-IDs or
-- BGP-LU advertisements). Empty by default — Network has no RD-bound BGP semantics.
--
-- Format (validated на app-layer): "ASN:NN" или "IPv4:NN" (например "65000:42").

ALTER TABLE networks
  ADD COLUMN IF NOT EXISTS route_distinguisher TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE networks DROP COLUMN IF EXISTS route_distinguisher;
