-- An invitation link is a bearer credential: whoever holds it registers a
-- portal account bound to a supplier, and from there sees that supplier's
-- contracts, orders, deliveries and evaluations. Until now the only thing that
-- ever ended one was expires_at — up to 14 days — so an invitation sent to a
-- mistyped address, or to a person who has since left the supplier, stayed
-- live and there was no statement anywhere that could stop it.
--
-- accepted_at already records the one ending the table knew about. revoked_at
-- records the other one: called back before it was used.
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS revoked_at timestamptz;

-- The list is read per supplier, from the modal that issues the links.
CREATE INDEX IF NOT EXISTS invitations_supplier_idx ON invitations(supplier_id, created_at DESC);
