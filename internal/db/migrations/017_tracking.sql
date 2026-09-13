-- Visitor tracking is configured on the administration screen, not at build
-- time: in a closed network the collector's address differs per installation
-- and changes while the service runs. The row is installed off. Turning it on
-- is what injects a snippet into the served pages and, with it, the policy
-- additions that let exactly that snippet run (internal/tracking).
--
-- momento is first and the default provider: it is the self-hosted collector,
-- the only choice under which nothing leaves the network. momentoProxy sends
-- the tracker through /momento/* on this origin so the collector never has to
-- appear in the content security policy at all.
INSERT INTO settings(key,value,category) VALUES
 ('tracking','{"enabled":false,"provider":"momento","momentoUrl":"","momentoSiteId":"","momentoProxy":true,"measurementId":"","matomoUrl":"","matomoSiteId":"","customSnippet":"","allowedHosts":"","includeAdmin":false,"placement":"head"}','tracking')
ON CONFLICT(key) DO NOTHING;
