package service

import (
	"fmt"
	pbsvc "github.com/hwsc-org/hwsc-api-blocks/int/hwsc-user-svc/user"
	pblib "github.com/hwsc-org/hwsc-api-blocks/lib"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"testing"
)

var (
	unitTestEmailTemplateDirectory = "../tmpl/"
	unitTestEmailCounter           = 1
	unitTestDefaultUser            = &pblib.User{
		FirstName:    "Unit Test",
		Organization: "Unit Testing",
	}
	unitTestFailValue = "shouldFail"
	unitTestFailEmail = "should@fail.com"
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

// TODO temporary, remove after removing pending token is implemented
func unitTestRemovePendingToken(uuid string) error {
	command := `DELETE FROM user_svc.pending_tokens WHERE uuid = $1`
	_, err := postgresDB.Exec(command, uuid)
	return err
}

func TestGetStatus(t *testing.T) {
	// test service state locker
	cases := []struct {
		request     *pbsvc.UserRequest
		serverState state
		expMsg      string
	}{
		{&pbsvc.UserRequest{}, available, codes.OK.String()},
		{&pbsvc.UserRequest{}, unavailable, codes.Unavailable.String()},
	}

	for _, c := range cases {
		serviceStateLocker.currentServiceState = c.serverState
		s := Service{}
		response, _ := s.GetStatus(context.TODO(), c.request)
		assert.Equal(t, c.expMsg, response.GetMessage())
	}

	serviceStateLocker.currentServiceState = available
	s := Service{}

	// test refreshDBConnection
	err := postgresDB.Close()
	assert.Nil(t, err)

	response, _ := s.GetStatus(context.TODO(), &pbsvc.UserRequest{})
	assert.Equal(t, codes.Unavailable.String(), response.GetMessage())

	// reconnect
	err = refreshDBConnection()
	assert.Nil(t, err)
}

func TestCreateUser(t *testing.T) {
	templateDirectory = unitTestEmailTemplateDirectory

	// valid
	testUser1 := unitTestUserGenerator("CreateUser-One")

	// valid
	testUser2 := unitTestUserGenerator("CreateUser-Two")

	// fail: duplicate email test
	testUser3 := &pblib.User{
		FirstName:    unitTestDefaultUser.GetFirstName(),
		LastName:     "CreateUser Fail",
		Email:        testUser1.GetEmail(),
		Password:     unitTestFailValue,
		Organization: unitTestDefaultUser.GetOrganization(),
	}

	// fail: invalid fields in userobject (it will fail on firstname)
	testUser4 := &pblib.User{
		FirstName: "",
	}

	// fail: empty password
	testUser5 := &pblib.User{
		FirstName: unitTestDefaultUser.GetFirstName(),
		LastName:  "CreateUser Fail",
		Email:     unitTestFailEmail,
		Password:  "",
	}

	// fail: blank email
	testUser7 := &pblib.User{
		FirstName: unitTestDefaultUser.GetFirstName(),
		LastName:  "CreateUser Fail",
		Email:     "",
	}

	// fail: blank organization
	testUser8 := &pblib.User{
		FirstName:    unitTestDefaultUser.GetFirstName(),
		LastName:     "CreateUser Fail",
		Email:        unitTestFailEmail,
		Password:     unitTestFailValue,
		Organization: "",
	}

	// fail: blank last name
	testUser9 := &pblib.User{
		FirstName: unitTestDefaultUser.GetFirstName(),
		LastName:  "",
	}

	cases := []struct {
		request  *pbsvc.UserRequest
		isExpErr bool
		expMsg   string
	}{
		{nil, true, "rpc error: code = InvalidArgument desc = nil request User"},
		{&pbsvc.UserRequest{}, true, "rpc error: code = InvalidArgument " +
			"desc = nil request User"},
		{&pbsvc.UserRequest{User: testUser1}, false, codes.OK.String()},
		{&pbsvc.UserRequest{User: testUser2}, false, codes.OK.String()},
		{&pbsvc.UserRequest{User: testUser3}, true, "rpc error: code = " +
			"Internal desc = pq: duplicate key value violates unique constraint \"accounts_email_key\""},
		{&pbsvc.UserRequest{User: testUser4}, true, "rpc error: code = " +
			"Internal desc = invalid User first name"},
		{&pbsvc.UserRequest{User: testUser5}, true, "rpc error: code = " +
			"Internal desc = invalid User password"},
		{&pbsvc.UserRequest{User: testUser7}, true, "rpc error: code = " +
			"Internal desc = invalid User email"},
		{&pbsvc.UserRequest{User: testUser8}, true, "rpc error: code = " +
			"Internal desc = invalid User organization"},
		{&pbsvc.UserRequest{User: testUser9}, true, "rpc error: code = " +
			"Internal desc = invalid User last name"},
	}

	for _, c := range cases {
		s := Service{}
		response, err := s.CreateUser(context.TODO(), c.request)
		if c.isExpErr {
			assert.EqualError(t, err, c.expMsg)
		} else {
			assert.Equal(t, codes.OK.String(), response.GetMessage())
		}
	}
}

func TestDeleteUser(t *testing.T) {
	templateDirectory = unitTestEmailTemplateDirectory
	// insert valid user
	response, err := unitTestInsertUser("DeleteUser-One")
	assert.Nil(t, err)
	assert.Equal(t, codes.OK.String(), response.GetMessage())

	// generate a valid uuid to test non existent uuid in table
	uuid, err := generateUUID()
	assert.Nil(t, err)
	assert.NotNil(t, uuid)

	// existing uuid
	test1 := &pblib.User{
		Uuid: response.GetUser().GetUuid(),
	}

	// nonexistent uuid
	test2 := &pblib.User{
		Uuid: uuid,
	}

	// invalid uuid's
	test3 := &pblib.User{
		Uuid: "",
	}
	test4 := &pblib.User{
		Uuid: "01d1nba01gnzbrkbfrrvgrz2m",
	}

	cases := []struct {
		request  *pbsvc.UserRequest
		isExpErr bool
		expMsg   string
	}{
		{&pbsvc.UserRequest{User: test1}, false, codes.OK.String()},
		{&pbsvc.UserRequest{User: test2}, false, codes.OK.String()},
		{&pbsvc.UserRequest{User: test3}, true,
			"rpc error: code = InvalidArgument desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: test4}, true,
			"rpc error: code = InvalidArgument desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: nil}, true,
			"rpc error: code = InvalidArgument desc = nil request User"},
		{nil, true, "rpc error: code = InvalidArgument desc = nil request User"},
	}

	for _, c := range cases {
		s := Service{}
		response, err := s.DeleteUser(context.TODO(), c.request)
		if c.isExpErr {
			assert.EqualError(t, err, c.expMsg)
			assert.Nil(t, response)
		} else {
			assert.Equal(t, codes.OK.String(), response.GetMessage())
			assert.Nil(t, err)
		}
	}
}

func TestGetUser(t *testing.T) {
	templateDirectory = unitTestEmailTemplateDirectory

	// insert valid user
	response, err := unitTestInsertUser("GetUser-One")
	assert.Nil(t, err)
	assert.Equal(t, codes.OK.String(), response.GetMessage())

	// generate a valid uuid to test non existent uuid in table
	uuid, err := generateUUID()
	assert.Nil(t, err)
	assert.NotNil(t, uuid)

	// exisiting uuid
	test1 := &pblib.User{
		Uuid: response.GetUser().GetUuid(),
	}

	// nonexistent uuid
	test2 := &pblib.User{
		Uuid: uuid,
	}

	cases := []struct {
		request  *pbsvc.UserRequest
		isExpErr bool
		expMsg   string
	}{
		{&pbsvc.UserRequest{User: test1}, false, ""},
		{&pbsvc.UserRequest{User: test2}, true,
			"rpc error: code = Internal desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: nil}, true,
			"rpc error: code = InvalidArgument desc = nil request User"},
		{nil, true, "rpc error: code = InvalidArgument desc = nil request User"},
	}

	for _, c := range cases {
		s := Service{}
		response, err := s.GetUser(context.TODO(), c.request)

		if c.isExpErr {
			assert.EqualError(t, err, c.expMsg)
			assert.Nil(t, response)
		} else {
			assert.Nil(t, err)
			assert.Equal(t, codes.OK.String(), response.GetMessage())
		}
	}
}

func TestUpdateUser(t *testing.T) {
	templateDirectory = unitTestEmailTemplateDirectory

	// insert valid user 1
	response1, err := unitTestInsertUser("UpdateUser-One")
	assert.Nil(t, err)
	assert.Equal(t, codes.OK.String(), response1.GetMessage())

	// insert valid user 2
	response2, err := unitTestInsertUser("UpdateUser-Two")
	assert.Nil(t, err)
	assert.Equal(t, codes.OK.String(), response2.GetMessage())

	// TODO force remove pending token until we have a service for removing pending tokens
	err = unitTestRemovePendingToken(response2.GetUser().GetUuid())
	assert.Nil(t, err)

	nonExistingUUID, err := generateUUID()
	assert.Nil(t, err)

	// valid response1
	// prospective email is NULL
	// modified_date set
	updateUser := &pblib.User{
		LastName:     response1.GetUser().GetLastName() + " UPDATED",
		Password:     "newPassword",
		Organization: response1.GetUser().GetOrganization() + " UPDATED",
		Uuid:         response1.GetUser().GetUuid(),
	}

	// valid response2
	// test prospective_email is set
	// modified_date set
	updateUser2 := &pblib.User{
		LastName: response1.GetUser().GetLastName() + " UPDATED",
		Email:    response2.GetUser().GetEmail() + "UPDATED",
		Uuid:     response2.GetUser().GetUuid(),
	}

	// fail - invalid uuid
	updateUser3 := &pblib.User{
		LastName: unitTestFailValue,
		Uuid:     "0000xsnjg0mqjhbf4qx",
	}

	// fail - non-existent uuid (uuid is in valid format)
	updateUser4 := &pblib.User{
		LastName: unitTestFailValue,
		Uuid:     nonExistingUUID,
	}

	// fail - invalid email format
	updateUser5 := &pblib.User{
		LastName: unitTestFailValue,
		Email:    "a",
		Uuid:     response2.GetUser().GetUuid(),
	}

	// fail - invalid first name
	updateUser6 := &pblib.User{
		FirstName: "@@@",
		Uuid:      response2.GetUser().GetUuid(),
	}

	// fail - invalid last name
	updateUser7 := &pblib.User{
		LastName: "@@@",
		Uuid:     response2.GetUser().GetUuid(),
	}

	cases := []struct {
		request  *pbsvc.UserRequest
		isExpErr bool
		expMsg   string
	}{
		{&pbsvc.UserRequest{User: updateUser}, false, ""},
		{&pbsvc.UserRequest{User: updateUser2}, false, ""},
		{nil, true, "rpc error: code = InvalidArgument desc = nil request User"},
		{&pbsvc.UserRequest{User: updateUser3}, true,
			"rpc error: code = InvalidArgument desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: updateUser4}, true,
			"rpc error: code = Internal desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: updateUser5}, true,
			"rpc error: code = Internal desc = invalid User email"},
		{&pbsvc.UserRequest{User: updateUser6}, true,
			"rpc error: code = Internal desc = invalid User first name"},
		{&pbsvc.UserRequest{User: updateUser7}, true,
			"rpc error: code = Internal desc = invalid User last name"},
		{&pbsvc.UserRequest{User: nil}, true,
			"rpc error: code = InvalidArgument desc = nil request User"},
	}

	for _, c := range cases {
		s := Service{}
		response, err := s.UpdateUser(context.TODO(), c.request)

		if c.isExpErr {
			assert.EqualError(t, err, c.expMsg)
			assert.Nil(t, response)
		} else {
			assert.Nil(t, err)
			assert.Equal(t, codes.OK.String(), response.GetMessage())
		}
	}
}

func TestAuthenticateUser(t *testing.T) {
	templateDirectory = unitTestEmailTemplateDirectory

	validPassword := "AuthenticateUser-One"

	response, err := unitTestInsertUser(validPassword)
	assert.Nil(t, err)
	assert.Equal(t, codes.OK.String(), response.Message)

	createdUser := response.GetUser()
	assert.NotNil(t, createdUser)

	validUUID := createdUser.GetUuid()
	validEmail := createdUser.GetEmail()

	nonExistingUUID, err := generateUUID()
	assert.Nil(t, err)

	// valid user
	validUser := &pblib.User{
		Uuid:     validUUID,
		Email:    validEmail,
		Password: validPassword,
	}

	// non existing uuid
	invalidUser1 := &pblib.User{
		Uuid:     nonExistingUUID,
		Email:    validEmail,
		Password: validPassword,
	}

	// non existing email
	invalidUser2 := &pblib.User{
		Uuid:     validUUID,
		Email:    unitTestFailEmail,
		Password: validPassword,
	}

	// non matching password
	invalidUser3 := &pblib.User{
		Uuid:     validUUID,
		Email:    validEmail,
		Password: unitTestFailValue,
	}

	// invalid uuid form
	invalidUser4 := &pblib.User{
		Uuid:     "0000xsnjg0mq",
		Email:    validEmail,
		Password: validPassword,
	}

	// invalid email form
	invalidUser5 := &pblib.User{
		Uuid:     validUUID,
		Email:    "@",
		Password: validPassword,
	}

	// invalid password
	invalidUser6 := &pblib.User{
		Uuid:     validUUID,
		Email:    validEmail,
		Password: "",
	}

	// missing uuid
	invalidUser7 := &pblib.User{
		Email:    validEmail,
		Password: validPassword,
	}

	// missing email
	invalidUser8 := &pblib.User{
		Uuid:     validUUID,
		Password: validPassword,
	}

	// missing password
	invalidUser9 := &pblib.User{
		Uuid:  validUUID,
		Email: validEmail,
	}

	cases := []struct {
		request  *pbsvc.UserRequest
		isExpErr bool
		expMsg   string
	}{
		{&pbsvc.UserRequest{User: validUser}, false, ""},
		{nil, true, "rpc error: code = InvalidArgument desc = nil request User"},
		{&pbsvc.UserRequest{User: nil}, true,
			"rpc error: code = InvalidArgument desc = nil request User"},
		{&pbsvc.UserRequest{User: invalidUser1}, true,
			"rpc error: code = Unknown desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: invalidUser2}, true,
			"rpc error: code = InvalidArgument desc = email does not match"},
		{&pbsvc.UserRequest{User: invalidUser3}, true,
			"rpc error: code = Unauthenticated desc = " +
				"crypto/bcrypt: hashedPassword is not the hash of the given password"},
		{&pbsvc.UserRequest{User: invalidUser4}, true,
			"rpc error: code = InvalidArgument desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: invalidUser5}, true,
			"rpc error: code = InvalidArgument desc = invalid User email"},
		{&pbsvc.UserRequest{User: invalidUser6}, true,
			"rpc error: code = InvalidArgument desc = invalid User password"},
		{&pbsvc.UserRequest{User: invalidUser7}, true,
			"rpc error: code = InvalidArgument desc = invalid User uuid"},
		{&pbsvc.UserRequest{User: invalidUser8}, true,
			"rpc error: code = InvalidArgument desc = invalid User email"},
		{&pbsvc.UserRequest{User: invalidUser9}, true,
			"rpc error: code = InvalidArgument desc = invalid User password"},
	}

	for _, c := range cases {
		s := Service{}
		response, err := s.AuthenticateUser(context.TODO(), c.request)
		if c.isExpErr {
			assert.Nil(t, response)
			assert.EqualError(t, err, c.expMsg)
		} else {
			assert.Nil(t, err)
			assert.Equal(t, codes.OK.String(), response.Message)
		}
	}
}
