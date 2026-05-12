-- +goose Up
-- vpn_id — 24-bit data-plane идентификатор сети (epic KAC-2). Аллоцируется на
-- Network.Create, возвращается во free-list на Network.Delete. ИНФРА-ЧУВСТВИТЕЛЬНОЕ:
-- отдаётся только через InternalNetworkService.GetNetwork, не на публичном Network
-- (см. workspace CLAUDE.md §«Инфра-чувствительные данные»).
CREATE SEQUENCE vpn_id_seq START 1 MINVALUE 1 MAXVALUE 16777215;

ALTER TABLE networks ADD COLUMN vpn_id integer;
UPDATE networks SET vpn_id = nextval('vpn_id_seq') WHERE vpn_id IS NULL;
ALTER TABLE networks ALTER COLUMN vpn_id SET NOT NULL;
ALTER TABLE networks ADD CONSTRAINT networks_vpn_id_key UNIQUE (vpn_id);

-- free-list освобождённых vpn_id (переиспользуются на следующей аллокации).
CREATE TABLE vpn_id_free (id integer PRIMARY KEY);

-- +goose Down
DROP TABLE vpn_id_free;
ALTER TABLE networks DROP CONSTRAINT networks_vpn_id_key;
ALTER TABLE networks DROP COLUMN vpn_id;
DROP SEQUENCE vpn_id_seq;
