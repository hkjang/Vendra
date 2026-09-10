-- Four of the roles the product ships with name permissions the application
-- never checks.
--
-- A role's permissions are only ever read on the wanted side of hasPermission,
-- so a word no door asks for is not a narrower permission but no permission at
-- all — and in the role list it looks exactly like one that works.
-- "risk.security.*", "evaluation.security.*", "risk.contract.*",
-- "contract.review" and "risk.compliance.*" are all the same shape: a
-- per-subject granularity the gate does not have. The doors are risk.read,
-- risk.create, evaluation.read, evaluation.create and contract.read.
--
-- What that cost: 보안 담당자 was given the security risk permission and cannot
-- file a risk, because risk.create is the door and "risk.security.*" reaches
-- nothing. 계약 담당자 was given the contract risk permission and cannot see a
-- risk at all — the role carries no other risk word. 준법·법무 already holds
-- contract.read and risk.read, so its two dead words cost nothing but say
-- something untrue about what the role can do.
--
-- Each dead word is dropped and the door it evidently meant is added, and only
-- where the role still carries the word. Nothing else is widened, and the
-- permissions are checked now when they are written, so a catalogue cannot
-- drift this way again.

-- 보안 담당자: reading risks and evaluations it already has; filing them is
-- what "risk.security.*" and "evaluation.security.*" were standing in for.
UPDATE roles SET permissions=(permissions - 'risk.security.*' - 'evaluation.security.*')
  || (CASE WHEN permissions ? 'risk.create' THEN '[]' ELSE '["risk.create"]' END)::jsonb
  || (CASE WHEN permissions ? 'evaluation.create' THEN '[]' ELSE '["evaluation.create"]' END)::jsonb
 WHERE code='security' AND (permissions ? 'risk.security.*' OR permissions ? 'evaluation.security.*');

-- 계약 담당자: "risk.contract.*" was the only risk word the role had, so the
-- contract risks it was given were invisible to it.
UPDATE roles SET permissions=(permissions - 'risk.contract.*')
  || (CASE WHEN permissions ? 'risk.read' THEN '[]' ELSE '["risk.read"]' END)::jsonb
 WHERE code='contract_manager' AND permissions ? 'risk.contract.*';

-- 준법·법무: both words are already covered by contract.read and risk.read,
-- which the role carries, so these only come off.
UPDATE roles SET permissions=(permissions - 'contract.review' - 'risk.compliance.*')
 WHERE code='legal' AND (permissions ? 'contract.review' OR permissions ? 'risk.compliance.*');
