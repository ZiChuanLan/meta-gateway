CREATE TABLE operator_preferences (
 id INTEGER PRIMARY KEY CHECK(id=1),
 admin_username TEXT NOT NULL DEFAULT '',
 update_channel TEXT NOT NULL DEFAULT 'stable' CHECK(update_channel IN ('stable','beta'))
);
INSERT INTO operator_preferences(id) VALUES(1);

CREATE TRIGGER operator_username_unique BEFORE UPDATE OF admin_username ON operator_preferences
WHEN NEW.admin_username <> '' AND EXISTS(SELECT 1 FROM team_users WHERE lower(username)=lower(NEW.admin_username))
BEGIN SELECT RAISE(ABORT, 'username already exists'); END;
CREATE TRIGGER team_operator_name_insert BEFORE INSERT ON team_users
WHEN EXISTS(SELECT 1 FROM operator_preferences WHERE admin_username<>'' AND lower(admin_username)=lower(NEW.username))
BEGIN SELECT RAISE(ABORT, 'username already exists'); END;
CREATE TRIGGER team_operator_name_update BEFORE UPDATE OF username ON team_users
WHEN EXISTS(SELECT 1 FROM operator_preferences WHERE admin_username<>'' AND lower(admin_username)=lower(NEW.username))
BEGIN SELECT RAISE(ABORT, 'username already exists'); END;
