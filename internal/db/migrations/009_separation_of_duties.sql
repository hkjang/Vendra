INSERT INTO settings(key,value,category) VALUES
 ('workflow.separation_of_duties','{"blockSelfApproval":false}','workflow')
ON CONFLICT(key) DO NOTHING;
