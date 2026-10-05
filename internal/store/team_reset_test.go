package store_test

import "testing"

func TestFactoryResetClearsNumericTeamGrantsBeforeIDsAreReused(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`UPDATE team_policies SET members_json='[1,2]',models_json='["model"]',all_models=1 WHERE id=1;
		INSERT INTO team_users(username,name,password_hash,role,policy_id) VALUES('owner','Owner','test-hash','owner',1);
		INSERT INTO team_sessions(token_hash,user_id,version,created_at,expires_at) VALUES('test',1,1,1,9999999999);
		INSERT INTO team_route_plans(user_id,name,members_json) VALUES(1,'old','[{"id":1,"priority":100,"weight":100}]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FactoryReset(); err != nil {
		t.Fatal(err)
	}
	var members string
	var all, sessions, plans int
	if err := db.QueryRow(`SELECT members_json,all_models FROM team_policies WHERE id=1`).Scan(&members, &all); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT count(*) FROM team_sessions`).Scan(&sessions)
	db.QueryRow(`SELECT count(*) FROM team_route_plans`).Scan(&plans)
	if members != "[]" || all != 0 || sessions != 0 || plans != 0 {
		t.Fatalf("grants survived reset: %s %d %d %d", members, all, sessions, plans)
	}
}
