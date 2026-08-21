INSERT INTO settings(key,value,category) VALUES
 ('security.password','{"minLength":10,"requireClasses":0}','security')
ON CONFLICT(key) DO NOTHING;
