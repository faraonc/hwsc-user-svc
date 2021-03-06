package service

import (
	"fmt"
	pbsvc "github.com/hwsc-org/hwsc-api-blocks/protobuf/hwsc-user-svc/user"
	pblib "github.com/hwsc-org/hwsc-api-blocks/protobuf/lib"
	"github.com/hwsc-org/hwsc-lib/auth"
	"golang.org/x/net/context"
	"time"
)

var (
	unitTestFailValue    = "shouldFail"
	unitTestFailEmail    = "should@fail.com"
	unitTestEmailCounter = 1
	unitTestDefaultUser  = &pblib.User{
		FirstName:    "Unit Test",
		Organization: "Unit Testing",
	}

	validAuthTokenHeader = &auth.Header{
		Alg:      auth.Hs256,
		TokenTyp: auth.Jwt,
	}

	validUUID, _ = generateUUID()

	validAuthTokenBody = &auth.Body{
		UUID:                validUUID,
		Permission:          auth.User,
		ExpirationTimestamp: time.Now().UTC().Add(time.Hour * time.Duration(authTokenExpirationTime)).Unix(),
	}

	validNoUUIDAuthTokenBody = &auth.Body{
		Permission:          auth.User,
		ExpirationTimestamp: time.Now().UTC().Add(time.Hour * time.Duration(authTokenExpirationTime)).Unix(),
	}
)

func unitTestEmailGenerator() string {
	email := "hwsc.test+user" + fmt.Sprint(unitTestEmailCounter) + "@gmail.com"
	unitTestEmailCounter++

	return email
}

func unitTestUserGenerator(lastName string) *pblib.User {
	return &pblib.User{
		FirstName:    unitTestDefaultUser.GetFirstName(),
		LastName:     lastName,
		Email:        unitTestEmailGenerator(),
		Password:     lastName,
		Organization: unitTestDefaultUser.Organization,
	}
}

func unitTestInsertUser(lastName string) (*pbsvc.UserResponse, error) {
	insertUser := unitTestUserGenerator(lastName)
	s := Service{}

	return s.CreateUser(context.TODO(), &pbsvc.UserRequest{User: insertUser})
}

func unitTestDeleteAuthSecretTable() error {
	_, err := postgresDB.Exec("DELETE FROM user_security.secrets")
	if err != nil {
		return err
	}

	// active_secret is set to ON CASCADE DELETE, if foregin key (secret_key)
	// it references from secrets table is deleted, but just in case
	_, err = postgresDB.Exec("DELETE FROM user_security.active_secret")

	currAuthSecret = nil
	return err
}

func unitTestDeleteInsertGetAuthSecret() (*pblib.Secret, error) {
	if err := unitTestDeleteAuthSecretTable(); err != nil {
		return nil, err
	}

	if err := insertNewAuthSecret(); err != nil {
		return nil, err
	}

	return getActiveSecretRow()
}

func unitTestInsertNewAuthToken() (*pblib.Secret, string, error) {
	// delete tokens table
	_, err := postgresDB.Exec("DELETE FROM user_security.auth_tokens")
	if err != nil {
		return nil, "", err
	}

	// delete secrets table and generate a new secret
	newSecret, err := unitTestDeleteInsertGetAuthSecret()
	if err != nil {
		return nil, "", err
	}
	time.Sleep(2 * time.Second)

	validUUID, err := generateUUID()
	if err != nil {
		return nil, "", err
	}
	validNoUUIDAuthTokenBody.UUID = validUUID

	// generate new token
	newToken, err := auth.NewToken(validAuthTokenHeader, validNoUUIDAuthTokenBody, newSecret)
	if err != nil {
		return nil, "", err
	}

	// insert a token
	if err := insertAuthToken(newToken, validAuthTokenHeader, validNoUUIDAuthTokenBody, newSecret); err != nil {
		return nil, "", err
	}

	return newSecret, newToken, nil
}
