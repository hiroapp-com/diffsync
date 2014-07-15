package diffsync

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUserSerialize(t *testing.T) {
	ts := time.Now()
	user := User{UID: "uid-test",
		Name:        "name-test",
		Email:       "test@hiroapp.com",
		Phone:       "+100012345",
		EmailStatus: "verified",
		PhoneStatus: "unverified",
		Plan:        "starter",
		Tier:        1,
		SignupAt:    &ts,
	}
	res, err := json.Marshal(user)
	if assert.NoError(t, err, "cannot jsonify user") {
		err = json.Unmarshal(res, &user)
		if assert.NoError(t, err, "cannot parse user json") {
			assert.Equal(t, "uid-test", user.UID, "uid mismatch after serializaion")
			assert.Equal(t, "name-test", user.Name, "name mismatch after serializaion")
			assert.Equal(t, "test@hiroapp.com", user.Email, "email mismatch after serializaion")
			assert.Equal(t, "+100012345", user.Phone, "phone mismatch after serializaion")
			assert.Equal(t, "verified", user.EmailStatus, "email_status mismatch after serializaion")
			assert.Equal(t, "unverified", user.PhoneStatus, "phone_status mismatch after serializaion")
			assert.Equal(t, "starter", user.Plan, "plan mismatch after serializaion")
			assert.Equal(t, 1, user.Tier, "tier mismatch after serializaion")
			assert.Equal(t, ts, *user.SignupAt, "signup_at mismatch after serializaion")
		}
	}
}
