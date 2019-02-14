package service

import (
	pbsvc "github.com/hwsc-org/hwsc-api-blocks/int/hwsc-user-svc/user"
	pblib "github.com/hwsc-org/hwsc-api-blocks/lib"
	"github.com/hwsc-org/hwsc-lib/logger"
	"github.com/hwsc-org/hwsc-user-svc/conf"
	"github.com/hwsc-org/hwsc-user-svc/consts"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
)

// Service struct type, implements the generated (pb file) UserServiceServer interface
type Service struct{}

// state of the service
type state uint32

// stateLocker synchronizes the state of the service
type stateLocker struct {
	lock                sync.RWMutex
	currentServiceState state
}

const (
	// available - service is ready and available for read/write
	available state = 0

	// unavailable - service is locked
	unavailable state = 1
)

var (
	serviceStateLocker stateLocker
	uuidMapLocker      sync.Map

	// converts the state of the service to a string
	serviceStateMap = map[state]string{
		available:   "Available",
		unavailable: "Unavailable",
	}
)

func init() {
	serviceStateLocker = stateLocker{
		currentServiceState: available,
	}
}

// GetStatus gets the current status of the service
// Returns status code int and status code text, and any connection errors
func (s *Service) GetStatus(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("GetStatus")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		return consts.ResponseServiceUnavailable, nil
	}

	if err := refreshDBConnection(); err != nil {
		return consts.ResponseServiceUnavailable, nil
	}

	return &pbsvc.UserResponse{
		Status:  &pbsvc.UserResponse_Code{Code: uint32(codes.OK)},
		Message: codes.OK.String(),
	}, nil
}

// CreateUser creates a new user document and inserts it to user DB
func (s *Service) CreateUser(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("CreateUser")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		logger.Error(consts.CreateUserTag, consts.ErrServiceUnavailable.Error())
		return nil, consts.ErrStatusServiceUnavailable
	}

	if req == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := refreshDBConnection(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// get User Object
	user := req.GetUser()
	if user == nil {
		logger.Error(consts.ErrNilRequestUser.Error())
		return nil, consts.ErrStatusNilRequestUser
	}

	// generate uuid synchronously to prevent users getting the same uuid
	var err error
	user.Uuid, err = generateUUID()
	if err != nil {
		logger.Error(consts.CreateUserTag, consts.MsgErrGeneratingUUID, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// sync.Map equivalent to map[string](&sync.RWMutex{}) = each uuid string gets its own lock
	// LoadOrStore = LOAD: get the lock for uuid or if not exist,
	// 				 STORE: make uuid key and store lock type &sync.RWMutex{}
	lock, _ := uuidMapLocker.LoadOrStore(user.GetUuid(), &sync.RWMutex{})
	lock.(*sync.RWMutex).Lock()
	defer lock.(*sync.RWMutex).Unlock()

	// insert user into DB
	if err := insertNewUser(user); err != nil {
		// remove unstored/invaid uuid from cache uuidMapLocker b/c
		// Mutex was allocated (saves resources/memory and prevent security issues)
		uuidMapLocker.Delete(user.GetUuid())
		logger.Error(consts.CreateUserTag, consts.MsgErrInsertUser, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// insert token into db
	if err := insertEmailToken(user.GetUuid()); err != nil {
		logger.Error(consts.CreateUserTag, consts.MsgErrInsertToken, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// send email
	emailReq, err := newEmailRequest(nil, []string{user.GetEmail()}, conf.EmailHost.Username, subjectVerifyEmail)
	if err != nil {
		logger.Error(consts.CreateUserTag, consts.MsgErrEmailRequest, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	if err := emailReq.sendEmail(templateVerifyEmail); err != nil {
		logger.Error(consts.CreateUserTag, consts.MsgErrSendEmail, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Info("Inserted new user:", user.GetUuid(), user.GetFirstName(), user.GetLastName())

	user.Password = ""
	user.IsVerified = false

	return &pbsvc.UserResponse{
		Status:  &pbsvc.UserResponse_Code{Code: uint32(codes.OK)},
		Message: codes.OK.String(),
		User:    user,
	}, nil
}

// DeleteUser deletes a user document in user DB
func (s *Service) DeleteUser(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("DeleteUser")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		logger.Error(consts.DeleteUserTag, consts.ErrServiceUnavailable.Error())
		return nil, consts.ErrStatusServiceUnavailable
	}

	if req == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := refreshDBConnection(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// get User Object
	user := req.GetUser()
	if user == nil {
		logger.Error(consts.ErrNilRequestUser.Error())
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := validateUUID(user.GetUuid()); err != nil {
		logger.Error(consts.DeleteUserTag, consts.ErrInvalidUUID.Error())
		return nil, consts.ErrStatusUUIDInvalid
	}

	lock, _ := uuidMapLocker.LoadOrStore(user.GetUuid(), &sync.RWMutex{})
	lock.(*sync.RWMutex).Lock()
	defer lock.(*sync.RWMutex).Unlock()

	// delete from db
	if err := deleteUserRow(user.GetUuid()); err != nil {
		logger.Error(consts.DeleteUserTag, consts.MsgErrDeleteUser, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Info("Deleted user:", user.GetUuid(), user.GetFirstName(), user.GetLastName())

	// release mutex resource
	uuidMapLocker.Delete(user.GetUuid())

	return &pbsvc.UserResponse{
		Status:  &pbsvc.UserResponse_Code{Code: uint32(codes.OK)},
		Message: codes.OK.String(),
		User:    &pblib.User{Uuid: user.GetUuid()},
	}, nil
}

// UpdateUser updates a user document in user DB
func (s *Service) UpdateUser(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("UpdateUser")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		logger.Error(consts.UpdateUserTag, consts.ErrServiceUnavailable.Error())
		return nil, consts.ErrStatusServiceUnavailable
	}

	if req == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := refreshDBConnection(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// get User Object
	svcDerivedUser := req.GetUser()
	if svcDerivedUser == nil {
		logger.Error(consts.ErrNilRequestUser.Error())
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := validateUUID(svcDerivedUser.GetUuid()); err != nil {
		logger.Error(consts.UpdateUserTag, consts.ErrInvalidUUID.Error())
		return nil, consts.ErrStatusUUIDInvalid
	}

	lock, _ := uuidMapLocker.LoadOrStore(svcDerivedUser.GetUuid(), &sync.RWMutex{})
	lock.(*sync.RWMutex).Lock()
	defer lock.(*sync.RWMutex).Unlock()

	// retrieve users row from database
	dbDerivedUser, err := getUserRow(svcDerivedUser.GetUuid())
	if err != nil {
		logger.Error(consts.UpdateUserTag, consts.MsgErrGetUserRow, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	if dbDerivedUser == nil {
		logger.Error(consts.UpdateUserTag, consts.ErrUUIDNotFound.Error())
		return nil, consts.ErrStatusUUIDNotFound
	}

	// update user
	var updatedUser *pblib.User
	updatedUser, err = updateUserRow(svcDerivedUser.GetUuid(), svcDerivedUser, dbDerivedUser)
	if err != nil {
		logger.Error(consts.UpdateUserTag, consts.MsgErrUpdateUserRow, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	logger.Info("Updated user:", updatedUser.GetUuid(),
		updatedUser.GetFirstName(), updatedUser.GetLastName())

	updatedUser.Password = ""
	return &pbsvc.UserResponse{
		Status:  &pbsvc.UserResponse_Code{Code: uint32(codes.OK)},
		Message: codes.OK.String(),
		User:    updatedUser,
	}, nil
}

// AuthenticateUser goes through user DB collection and tries to find matching email/password
func (s *Service) AuthenticateUser(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("AuthenticateUser")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		logger.Error(consts.AuthenticateUserTag, consts.ErrServiceUnavailable.Error())
		return nil, consts.ErrStatusServiceUnavailable
	}

	if req == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := refreshDBConnection(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	user := req.GetUser()
	if user == nil {
		logger.Error(consts.ErrNilRequestUser.Error())
		return nil, consts.ErrStatusNilRequestUser
	}

	// validate uuid, email, password
	if err := validateUUID(user.GetUuid()); err != nil {
		logger.Error(consts.AuthenticateUserTag, consts.ErrInvalidUUID.Error())
		return nil, consts.ErrStatusUUIDInvalid
	}
	if err := validateEmail(user.GetEmail()); err != nil {
		logger.Error(consts.AuthenticateUserTag, consts.ErrInvalidUserEmail.Error())
		return nil, status.Error(codes.InvalidArgument, consts.ErrInvalidUserEmail.Error())
	}
	if err := validatePassword(user.GetPassword()); err != nil {
		logger.Error(consts.AuthenticateUserTag, consts.ErrInvalidPassword.Error())
		return nil, status.Error(codes.InvalidArgument, consts.ErrInvalidPassword.Error())
	}

	lock, _ := uuidMapLocker.LoadOrStore(user.GetUuid(), &sync.RWMutex{})
	lock.(*sync.RWMutex).RLock()
	defer lock.(*sync.RWMutex).RUnlock()

	// look up email and password
	retrievedUser, err := getUserRow(user.GetUuid())
	if err != nil {
		logger.Error(consts.AuthenticateUserTag, consts.MsgErrAuthenticateUser, err.Error())
		return nil, status.Error(codes.Unknown, err.Error())
	}

	if retrievedUser.GetEmail() != user.GetEmail() {
		logger.Error(consts.AuthenticateUserTag, consts.MsgErrMatchEmail)
		return nil, status.Error(codes.InvalidArgument, consts.MsgErrMatchEmail)
	}

	if err := comparePassword(retrievedUser.GetPassword(), user.GetPassword()); err != nil {
		logger.Error(consts.AuthenticateUserTag, consts.MsgErrMatchPassword, err.Error())
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	logger.Info("Authenticated user:", retrievedUser.GetUuid(),
		retrievedUser.GetFirstName(), retrievedUser.GetLastName())

	retrievedUser.Password = ""
	return &pbsvc.UserResponse{
		Status:  &pbsvc.UserResponse_Code{Code: uint32(codes.OK)},
		Message: codes.OK.String(),
		User:    retrievedUser,
	}, nil
}

// ListUsers returns the user DB collection
func (s *Service) ListUsers(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	//TODO
	logger.RequestService("ListUsers")
	return &pbsvc.UserResponse{}, nil
}

// GetUser returns a user document in user DB
func (s *Service) GetUser(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("GetUser")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		logger.Error(consts.GetUserTag, consts.ErrServiceUnavailable.Error())
		return nil, consts.ErrStatusServiceUnavailable
	}

	if req == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := refreshDBConnection(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// get User Object
	user := req.GetUser()
	if user == nil {
		logger.Error(consts.ErrNilRequestUser.Error())
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := validateUUID(user.GetUuid()); err != nil {
		logger.Error(consts.GetUserTag, consts.ErrInvalidUUID.Error())
		return nil, consts.ErrStatusUUIDInvalid
	}

	// read lock, b/c we are only retrieving/reading from the DB
	lock, _ := uuidMapLocker.LoadOrStore(user.GetUuid(), &sync.RWMutex{})
	lock.(*sync.RWMutex).RLock()
	defer lock.(*sync.RWMutex).RUnlock()

	// retrieve users row from database
	retrievedUser, err := getUserRow(user.GetUuid())
	if err != nil {
		logger.Error(consts.GetUserTag, consts.MsgErrGetUserRow, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	if retrievedUser == nil {
		logger.Error(consts.GetUserTag, consts.ErrUUIDNotFound.Error())
		return nil, consts.ErrStatusUUIDNotFound
	}

	logger.Info("Retrieved user:", user.GetUuid(), user.GetFirstName(), user.GetLastName())

	retrievedUser.Password = ""
	return &pbsvc.UserResponse{
		Status:  &pbsvc.UserResponse_Code{Code: uint32(codes.OK)},
		Message: codes.OK.String(),
		User:    retrievedUser,
	}, nil
}

// ShareDocument updates user/s documents shared_to_me field in user DB
func (s *Service) ShareDocument(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	//TODO
	logger.RequestService("ShareDocument")
	return &pbsvc.UserResponse{}, nil
}

// GetSecret retrieves and returns the recent/active secret from the DB
func (s *Service) GetSecret(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	// TODO
	logger.RequestService("Get Secret")
	return &pbsvc.UserResponse{}, nil
}

// GetToken generates a token after verifying user's email and password,
// stores generated token related info in DB, returns said token
func (s *Service) GetToken(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	logger.RequestService("GetAuthToken")

	if ok := serviceStateLocker.isStateAvailable(); !ok {
		logger.Error(consts.GetAuthTokenTag, consts.ErrServiceUnavailable.Error())
		return nil, consts.ErrStatusServiceUnavailable
	}

	if req == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	if err := refreshDBConnection(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	user := req.GetUser()
	if user == nil {
		return nil, consts.ErrStatusNilRequestUser
	}

	// validate uuid, email, password
	if err := validateUUID(user.GetUuid()); err != nil {
		logger.Error(consts.GetAuthTokenTag, consts.ErrInvalidUUID.Error())
		return nil, consts.ErrStatusUUIDInvalid
	}
	if err := validateEmail(user.GetEmail()); err != nil {
		logger.Error(consts.GetAuthTokenTag, consts.ErrInvalidUserEmail.Error())
		return nil, status.Error(codes.InvalidArgument, consts.ErrInvalidUserEmail.Error())
	}
	if err := validatePassword(user.GetPassword()); err != nil {
		logger.Error(consts.GetAuthTokenTag, consts.ErrInvalidPassword.Error())
		return nil, status.Error(codes.InvalidArgument, consts.ErrInvalidPassword.Error())
	}

	// write lock b/c we are writing to DB
	lock, _ := uuidMapLocker.LoadOrStore(user.GetUuid(), &sync.RWMutex{})
	lock.(*sync.RWMutex).Lock()
	defer lock.(*sync.RWMutex).Unlock()

	// look up email and password
	retrievedUser, err := getUserRow(user.GetUuid())
	if err != nil {
		logger.Error(consts.GetAuthTokenTag, consts.MsgErrAuthenticateUser, err.Error())
		return nil, status.Error(codes.Unknown, err.Error())
	}

	if retrievedUser.GetEmail() != user.GetEmail() {
		logger.Error(consts.GetAuthTokenTag, consts.MsgErrMatchEmail)
		return nil, status.Error(codes.InvalidArgument, consts.MsgErrMatchEmail)
	}

	if err := comparePassword(retrievedUser.GetPassword(), user.GetPassword()); err != nil {
		logger.Error(consts.GetAuthTokenTag, consts.MsgErrMatchPassword, err.Error())
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	// TODO permission string "USER" should come from retrievedUser.permission_level
	//permission := auth.PermissionEnumMap["USER"]
	//algorithm := auth.AlgorithmMap[permission]
	//
	////create JWT header and payload
	//header := &auth.Header{
	//	Alg: algorithm,
	//	TokenTyp: auth.Jwt,
	//}
	//// expiration date is set by auth's NewToken
	//body := &auth.Body{
	//	UUID: retrievedUser.GetUuid(),
	//	Permission: permission,
	//}
	//
	//// make secret key to sign jwtoken
	//secretKey, err := generateToken(auth.SignatureBytesMap[algorithm])
	//if err != nil {
	//	logger.Error(consts.GetAuthTokenTag, consts.MsgErrGeneratingToken, err.Error())
	//	return nil, status.Error(codes.Internal, err.Error())
	//}
	//secret := &pblib.Secret{
	//	Key: secretKey,
	//	CreatedTimestamp: time.Now().UTC().Unix(),
	//}
	//
	//// get token
	//signedToken, err := auth.NewToken(header, body, secret)
	//if err != nil {
	//	logger.Error(consts.GetAuthTokenTag, consts.MsgErrGeneratingSignedToken, err.Error())
	//	return nil, status.Error(codes.Internal, err.Error())
	//}

	// insert JWT detail into DB

	return &pbsvc.UserResponse{}, nil
}

// VerifyToken checks if received token from Chrome is valid
func (s *Service) VerifyToken(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	// TODO
	logger.RequestService("Verify Token")
	return &pbsvc.UserResponse{}, nil
}

// NewSecret generates and inserts a new secret into DB
func (s *Service) NewSecret(ctx context.Context, req *pbsvc.UserRequest) (*pbsvc.UserResponse, error) {
	// TODO
	logger.RequestService("New Secret")
	return &pbsvc.UserResponse{}, nil
}
