// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platformvm

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gorilla/rpc/v2/json2"

	"github.com/ava-labs/gecko/database"
	"github.com/ava-labs/gecko/ids"
	"github.com/ava-labs/gecko/utils/crypto"
	"github.com/ava-labs/gecko/utils/formatting"
	"github.com/ava-labs/gecko/utils/json"
)

var (
	errMissingDecisionBlock = errors.New("should have a decision block within the past two blocks")
	errParsingID            = errors.New("error parsing ID")
	errGetAccount           = errors.New("error retrieving account information")
	errGetAccounts          = errors.New("error getting accounts controlled by specified user")
	errGetUser              = errors.New("error while getting user. Does user exist?")
	errNoMethodWithGenesis  = errors.New("no method was provided but genesis data was provided")
	errCreatingTransaction  = errors.New("problem while creating transaction")
	errNoDestination        = errors.New("call is missing field 'stakeDestination'")
	errNoSource             = errors.New("call is missing field 'stakeSource'")
	errGetStakeSource       = errors.New("couldn't get account specified in 'stakeSource'")
)

var key *crypto.PrivateKeySECP256K1R

func init() {
	cb58 := formatting.CB58{}
	err := cb58.FromString("24jUJ9vZexUM6expyMcT48LBx27k1m7xpraoV62oSQAHdziao5")
	if err != nil {
		panic(err)
	}
	factory := crypto.FactorySECP256K1R{}
	pk, err := factory.ToPrivateKey(cb58.Bytes)
	if err != nil {
		panic(err)
	}
	key = pk.(*crypto.PrivateKeySECP256K1R)
}

// Service defines the API calls that can be made to the platform chain
type Service struct{ vm *VM }

/*
 ******************************************************
 ******************* Get Subnets **********************
 ******************************************************
 */

// APISubnet is a representation of a subnet used in API calls
type APISubnet struct {
	// ID of the subnet
	ID ids.ID `json:"id"`

	// Each element of [ControlKeys] the address of a public key.
	// A transaction to add a validator to this subnet requires
	// signatures from [Threshold] of these keys to be valid.
	ControlKeys []ids.ShortID `json:"controlKeys"`
	Threshold   json.Uint16   `json:"threshold"`
}

// GetSubnetsArgs are the arguments to GetSubnet
type GetSubnetsArgs struct {
	// IDs of the subnets to retrieve information about
	// If omitted, gets all subnets
	IDs []ids.ID `json:"ids"`
}

// GetSubnetsResponse is the response from calling GetSubnets
type GetSubnetsResponse struct {
	// Each element is a subnet that exists
	// Null if there are no subnets other than the default subnet
	Subnets []APISubnet `json:"subnets"`
}

// GetSubnets returns the subnets whose ID are in [args.IDs]
// The response will not contain the default subnet
func (service *Service) GetSubnets(_ *http.Request, args *GetSubnetsArgs, response *GetSubnetsResponse) error {
	subnets, err := service.vm.getSubnets(service.vm.DB) // all subnets
	if err != nil {
		return fmt.Errorf("error getting subnets from database: %v", err)
	}

	getAll := len(args.IDs) == 0

	if getAll {
		response.Subnets = make([]APISubnet, len(subnets))
		for i, subnet := range subnets {
			response.Subnets[i] = APISubnet{
				ID:          subnet.ID,
				ControlKeys: subnet.ControlKeys,
				Threshold:   json.Uint16(subnet.Threshold),
			}
		}
		return nil
	}

	idsSet := ids.Set{}
	idsSet.Add(args.IDs...)
	for _, subnet := range subnets {
		if idsSet.Contains(subnet.ID) {
			response.Subnets = append(response.Subnets,
				APISubnet{
					ID:          subnet.ID,
					ControlKeys: subnet.ControlKeys,
					Threshold:   json.Uint16(subnet.Threshold),
				},
			)
		}
	}
	return nil
}

/*
 ******************************************************
 **************** Get/Sample Validators ***************
 ******************************************************
 */

// GetCurrentValidatorsArgs are the arguments for calling GetCurrentValidators
type GetCurrentValidatorsArgs struct {
	// Subnet we're listing the validators of
	// If omitted, defaults to default subnet
	SubnetID ids.ID `json:"subnetID"`
}

// GetCurrentValidatorsReply are the results from calling GetCurrentValidators
type GetCurrentValidatorsReply struct {
	Validators []APIValidator `json:"validators"`
}

// GetCurrentValidators returns the list of current validators
func (service *Service) GetCurrentValidators(_ *http.Request, args *GetCurrentValidatorsArgs, reply *GetCurrentValidatorsReply) error {
	service.vm.Ctx.Log.Debug("GetCurrentValidators called")

	if args.SubnetID.IsZero() {
		args.SubnetID = DefaultSubnetID
	}

	validators, err := service.vm.getCurrentValidators(service.vm.DB, args.SubnetID)
	if err != nil {
		return fmt.Errorf("couldn't get validators of subnet with ID %s. Does it exist?", args.SubnetID)
	}

	reply.Validators = make([]APIValidator, validators.Len())
	for i, tx := range validators.Txs {
		vdr := tx.Vdr()
		weight := json.Uint64(vdr.Weight())
		if args.SubnetID.Equals(DefaultSubnetID) {
			reply.Validators[i] = APIValidator{
				ID:          vdr.ID(),
				StartTime:   json.Uint64(tx.StartTime().Unix()),
				EndTime:     json.Uint64(tx.EndTime().Unix()),
				StakeAmount: &weight,
			}
		} else {
			reply.Validators[i] = APIValidator{
				ID:        vdr.ID(),
				StartTime: json.Uint64(tx.StartTime().Unix()),
				EndTime:   json.Uint64(tx.EndTime().Unix()),
				Weight:    &weight,
			}
		}
	}

	return nil
}

// GetPendingValidatorsArgs are the arguments for calling GetPendingValidators
type GetPendingValidatorsArgs struct {
	// Subnet we're getting the pending validators of
	// If omitted, defaults to default subnet
	SubnetID ids.ID `json:"subnetID"`
}

// GetPendingValidatorsReply are the results from calling GetPendingValidators
type GetPendingValidatorsReply struct {
	Validators []APIValidator `json:"validators"`
}

// GetPendingValidators returns the list of current validators
func (service *Service) GetPendingValidators(_ *http.Request, args *GetPendingValidatorsArgs, reply *GetPendingValidatorsReply) error {
	service.vm.Ctx.Log.Debug("GetPendingValidators called")

	if args.SubnetID.IsZero() {
		args.SubnetID = DefaultSubnetID
	}

	validators, err := service.vm.getPendingValidators(service.vm.DB, args.SubnetID)
	if err != nil {
		return fmt.Errorf("couldn't get validators of subnet with ID %s. Does it exist?", args.SubnetID)
	}

	reply.Validators = make([]APIValidator, validators.Len())
	for i, tx := range validators.Txs {
		vdr := tx.Vdr()
		weight := json.Uint64(vdr.Weight())
		if args.SubnetID.Equals(DefaultSubnetID) {
			reply.Validators[i] = APIValidator{
				ID:          vdr.ID(),
				StartTime:   json.Uint64(tx.StartTime().Unix()),
				EndTime:     json.Uint64(tx.EndTime().Unix()),
				StakeAmount: &weight,
			}
		} else {
			reply.Validators[i] = APIValidator{
				ID:        vdr.ID(),
				StartTime: json.Uint64(tx.StartTime().Unix()),
				EndTime:   json.Uint64(tx.EndTime().Unix()),
				Weight:    &weight,
			}
		}
	}

	return nil
}

// SampleValidatorsArgs are the arguments for calling SampleValidators
type SampleValidatorsArgs struct {
	// Number of validators in the sample
	Size json.Uint16 `json:"size"`

	// ID of subnet to sample validators from
	// If omitted, defaults to the default subnet
	SubnetID ids.ID `json:"subnetID"`
}

// SampleValidatorsReply are the results from calling Sample
type SampleValidatorsReply struct {
	Validators []ids.ShortID `json:"validators"`
}

// SampleValidators returns a sampling of the list of current validators
func (service *Service) SampleValidators(_ *http.Request, args *SampleValidatorsArgs, reply *SampleValidatorsReply) error {
	service.vm.Ctx.Log.Debug("Sample called with {Size = %d}", args.Size)

	if args.SubnetID.IsZero() {
		args.SubnetID = DefaultSubnetID
	}

	validators, ok := service.vm.Validators.GetValidatorSet(args.SubnetID)
	if !ok {
		return fmt.Errorf("couldn't get validators of subnet with ID %s. Does it exist?", args.SubnetID)
	}

	sample := validators.Sample(int(args.Size))
	if setLen := len(sample); setLen != int(args.Size) {
		return fmt.Errorf("current number of validators (%d) is insufficient to sample %d validators", setLen, args.Size)
	}

	reply.Validators = make([]ids.ShortID, int(args.Size))
	for i, vdr := range sample {
		reply.Validators[i] = vdr.ID()
	}

	ids.SortShortIDs(reply.Validators)
	return nil
}

/*
 ******************************************************
 *************** Get/Create Accounts ******************
 ******************************************************
 */

// GetAccountArgs are the arguments for calling GetAccount
type GetAccountArgs struct {
	// Address of the account we want the information about
	Address ids.ShortID `json:"address"`
}

// GetAccountReply is the response from calling GetAccount
type GetAccountReply struct {
	Address ids.ShortID `json:"address"`
	Nonce   json.Uint64 `json:"nonce"`
	Balance json.Uint64 `json:"balance"`
}

// GetAccount details given account ID
func (service *Service) GetAccount(_ *http.Request, args *GetAccountArgs, reply *GetAccountReply) error {
	account, err := service.vm.getAccount(service.vm.DB, args.Address)
	if err != nil && err != database.ErrNotFound {
		return errGetAccount
	} else if err == database.ErrNotFound {
		account = newAccount(args.Address, 0, 0)
	}

	reply.Address = account.Address
	reply.Balance = json.Uint64(account.Balance)
	reply.Nonce = json.Uint64(account.Nonce)
	return nil
}

// ListAccountsArgs are the arguments to ListAccounts
type ListAccountsArgs struct {
	// List all of the accounts controlled by this user
	Username string `json:"username"`
	Password string `json:"password"`
}

// ListAccountsReply is the reply from ListAccounts
type ListAccountsReply struct {
	Accounts []APIAccount `json:"accounts"`
}

// ListAccounts lists all of the accounts controlled by [args.Username]
func (service *Service) ListAccounts(_ *http.Request, args *ListAccountsArgs, reply *ListAccountsReply) error {
	service.vm.Ctx.Log.Debug("platform.listAccounts called for user '%s'", args.Username)

	// db holds the user's info that pertains to the Platform Chain
	userDB, err := service.vm.Ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return errGetUser
	}

	// The user
	user := user{
		db: userDB,
	}

	// IDs of accounts controlled by this user
	accountIDs, err := user.getAccountIDs()
	if err != nil {
		return errGetAccounts
	}

	var accounts []APIAccount
	for _, accountID := range accountIDs {
		account, err := service.vm.getAccount(service.vm.DB, accountID) // Get account whose ID is [accountID]
		if err != nil && err != database.ErrNotFound {
			service.vm.Ctx.Log.Error("couldn't get account from database: %v", err)
			continue
		} else if err == database.ErrNotFound {
			account = newAccount(accountID, 0, 0)
		}
		accounts = append(accounts, APIAccount{
			Address: accountID,
			Nonce:   json.Uint64(account.Nonce),
			Balance: json.Uint64(account.Balance),
		})
	}
	reply.Accounts = accounts
	return nil
}

// CreateAccountArgs are the arguments for calling CreateAccount
type CreateAccountArgs struct {
	// User that will control the newly created account
	Username string `json:"username"`

	// That user's password
	Password string `json:"password"`

	// The private key that controls the new account.
	// If omitted, will generate a new private key belonging
	// to the user.
	PrivateKey string `json:"privateKey"`
}

// CreateAccountReply are the response from calling CreateAccount
type CreateAccountReply struct {
	// Address of the newly created account
	Address ids.ShortID `json:"address"`
}

// CreateAccount creates a new account on the Platform Chain
// The account is controlled by [args.Username]
// The account's ID is [privKey].PublicKey().Address(), where [privKey] is a
// private key controlled by the user.
func (service *Service) CreateAccount(_ *http.Request, args *CreateAccountArgs, reply *CreateAccountReply) error {
	service.vm.Ctx.Log.Debug("platform.createAccount called for user '%s'", args.Username)

	// userDB holds the user's info that pertains to the Platform Chain
	userDB, err := service.vm.Ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return errGetUser
	}

	// The user creating a new account
	user := user{
		db: userDB,
	}

	// private key that controls the new account
	var privKey *crypto.PrivateKeySECP256K1R
	// If no private key supplied in args, create a new one
	if args.PrivateKey == "" {
		privKeyInt, err := service.vm.factory.NewPrivateKey() // The private key that controls the new account
		if err != nil {                                       // The account ID is [private key].PublicKey().Address()
			return errors.New("problem generating private key")
		}
		privKey = privKeyInt.(*crypto.PrivateKeySECP256K1R)
	} else { // parse provided private key
		byteFormatter := formatting.CB58{}
		err := byteFormatter.FromString(args.PrivateKey)
		if err != nil {
			return errors.New("problem while parsing privateKey")
		}
		pk, err := service.vm.factory.ToPrivateKey(byteFormatter.Bytes)
		if err != nil {
			return errors.New("problem while parsing privateKey")
		}
		privKey = pk.(*crypto.PrivateKeySECP256K1R)
	}

	if err := user.putAccount(privKey); err != nil { // Save the private key
		return errors.New("problem saving account")
	}

	reply.Address = privKey.PublicKey().Address()

	return nil
}

type genericTx struct {
	Tx interface{} `serialize:"true"`
}

/*
 ******************************************************
 ************ Add Validators to Subnets ***************
 ******************************************************
 */

// AddDefaultSubnetValidatorArgs are the arguments to AddDefaultSubnetValidator
type AddDefaultSubnetValidatorArgs struct {
	APIDefaultSubnetValidator

	// Next unused nonce of the account the staked $AVA and tx fee are paid from
	PayerNonce json.Uint64 `json:"payerNonce"`
}

// AddDefaultSubnetValidatorResponse is the response from a call to AddDefaultSubnetValidator
type AddDefaultSubnetValidatorResponse struct {
	// The unsigned transaction
	UnsignedTx formatting.CB58 `json:"unsignedTx"`
}

// AddDefaultSubnetValidator returns an unsigned transaction to add a validator to the default subnet
// The returned unsigned transaction should be signed using Sign()
func (service *Service) AddDefaultSubnetValidator(_ *http.Request, args *AddDefaultSubnetValidatorArgs, reply *AddDefaultSubnetValidatorResponse) error {
	service.vm.Ctx.Log.Debug("platform.AddDefaultSubnetValidator called")

	if args.ID.IsZero() { // If ID unspecified, use this node's ID as validator ID
		args.ID = service.vm.Ctx.NodeID
	}

	// Create the transaction
	tx := addDefaultSubnetValidatorTx{UnsignedAddDefaultSubnetValidatorTx: UnsignedAddDefaultSubnetValidatorTx{
		DurationValidator: DurationValidator{
			Validator: Validator{
				NodeID: args.ID,
				Wght:   args.weight(),
			},
			Start: uint64(args.StartTime),
			End:   uint64(args.EndTime),
		},
		Nonce:       uint64(args.PayerNonce),
		Destination: args.Destination,
		NetworkID:   service.vm.Ctx.NetworkID,
		Shares:      uint32(args.DelegationFeeRate),
	}}

	txBytes, err := Codec.Marshal(genericTx{Tx: &tx})
	if err != nil {
		return fmt.Errorf("problem while creating transaction: %w", err)
	}

	reply.UnsignedTx.Bytes = txBytes
	return nil
}

// AddDefaultSubnetDelegatorArgs are the arguments to AddDefaultSubnetDelegator
type AddDefaultSubnetDelegatorArgs struct {
	APIValidator

	Destination ids.ShortID `json:"destination"`

	// Next unused nonce of the account the staked $AVA and tx fee are paid from
	PayerNonce json.Uint64 `json:"payerNonce"`
}

// AddDefaultSubnetDelegatorResponse is the response from a call to AddDefaultSubnetDelegator
type AddDefaultSubnetDelegatorResponse struct {
	// The unsigned transaction
	UnsignedTx formatting.CB58 `json:"unsignedTx"`
}

// AddDefaultSubnetDelegator returns an unsigned transaction to add a delegator
// to the default subnet
// The returned unsigned transaction should be signed using Sign()
func (service *Service) AddDefaultSubnetDelegator(_ *http.Request, args *AddDefaultSubnetDelegatorArgs, reply *AddDefaultSubnetDelegatorResponse) error {
	service.vm.Ctx.Log.Debug("platform.AddDefaultSubnetDelegator called")

	if args.ID.IsZero() { // If ID unspecified, use this node's ID as validator ID
		args.ID = service.vm.Ctx.NodeID
	}

	// Create the transaction
	tx := addDefaultSubnetDelegatorTx{UnsignedAddDefaultSubnetDelegatorTx: UnsignedAddDefaultSubnetDelegatorTx{
		DurationValidator: DurationValidator{
			Validator: Validator{
				NodeID: args.ID,
				Wght:   args.weight(),
			},
			Start: uint64(args.StartTime),
			End:   uint64(args.EndTime),
		},
		NetworkID:   service.vm.Ctx.NetworkID,
		Nonce:       uint64(args.PayerNonce),
		Destination: args.Destination,
	}}

	txBytes, err := Codec.Marshal(genericTx{Tx: &tx})
	if err != nil {
		return fmt.Errorf("problem while creating transaction: %w", err)
	}

	reply.UnsignedTx.Bytes = txBytes
	return nil
}

// AddNonDefaultSubnetValidatorArgs are the arguments to AddNonDefaultSubnetValidator
type AddNonDefaultSubnetValidatorArgs struct {
	APIValidator

	// ID of subnet to validate
	SubnetID ids.ID `json:"subnetID"`

	// Next unused nonce of the account the tx fee is paid from
	PayerNonce json.Uint64 `json:"payerNonce"`
}

// AddNonDefaultSubnetValidatorResponse is the response from a call to AddNonDefaultSubnetValidator
type AddNonDefaultSubnetValidatorResponse struct {
	// The unsigned transaction
	UnsignedTx formatting.CB58 `json:"unsignedTx"`
}

// AddNonDefaultSubnetValidator adds a validator to a subnet other than the default subnet
// Returns the unsigned transaction, which must be signed using Sign
func (service *Service) AddNonDefaultSubnetValidator(_ *http.Request, args *AddNonDefaultSubnetValidatorArgs, response *AddNonDefaultSubnetValidatorResponse) error {
	tx := addNonDefaultSubnetValidatorTx{
		UnsignedAddNonDefaultSubnetValidatorTx: UnsignedAddNonDefaultSubnetValidatorTx{
			SubnetValidator: SubnetValidator{
				DurationValidator: DurationValidator{
					Validator: Validator{
						NodeID: args.APIValidator.ID,
						Wght:   args.weight(),
					},
					Start: uint64(args.StartTime),
					End:   uint64(args.EndTime),
				},
				Subnet: args.SubnetID,
			},
			NetworkID: service.vm.Ctx.NetworkID,
			Nonce:     uint64(args.PayerNonce),
		},
		ControlSigs: nil,
		PayerSig:    [crypto.SECP256K1RSigLen]byte{},
		vm:          nil,
		id:          ids.ID{},
		senderID:    ids.ShortID{},
		bytes:       nil,
	}

	txBytes, err := Codec.Marshal(genericTx{Tx: &tx})
	if err != nil {
		return errCreatingTransaction
	}

	response.UnsignedTx.Bytes = txBytes
	return nil
}

/*
 ******************************************************
 **************** Sign/Issue Txs **********************
 ******************************************************
 */

// SignArgs are the arguments to Sign
type SignArgs struct {
	// The bytes to sign
	// Must be the output of AddDefaultSubnetValidator
	Tx formatting.CB58 `json:"tx"`

	// The address of the key signing the bytes
	Signer ids.ShortID `json:"signer"`

	// User that controls Signer
	Username string `json:"username"`
	Password string `json:"password"`
}

// SignResponse is the response from Sign
type SignResponse struct {
	// The signed bytes
	Tx formatting.CB58
}

// Sign [args.bytes]
func (service *Service) Sign(_ *http.Request, args *SignArgs, reply *SignResponse) error {
	service.vm.Ctx.Log.Debug("platform.sign called")

	// Get the key of the Signer
	db, err := service.vm.Ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return fmt.Errorf("couldn't get data for user '%s'. Does user exist?", args.Username)
	}
	user := user{db: db}

	key, err := user.getKey(args.Signer) // Key of [args.Signer]
	if err != nil {
		return errDB
	}
	if !bytes.Equal(key.PublicKey().Address().Bytes(), args.Signer.Bytes()) { // sanity check
		return errors.New("got unexpected key from database")
	}

	genTx := genericTx{}
	if err := Codec.Unmarshal(args.Tx.Bytes, &genTx); err != nil {
		return err
	}

	switch tx := genTx.Tx.(type) {
	case *addDefaultSubnetValidatorTx:
		genTx.Tx, err = service.signAddDefaultSubnetValidatorTx(tx, key)
	case *addDefaultSubnetDelegatorTx:
		genTx.Tx, err = service.signAddDefaultSubnetDelegatorTx(tx, key)
	case *addNonDefaultSubnetValidatorTx:
		genTx.Tx, err = service.signAddNonDefaultSubnetValidatorTx(tx, key)
	case *CreateSubnetTx:
		genTx.Tx, err = service.signCreateSubnetTx(tx, key)
	default:
		err = errors.New("Could not parse given tx. Must be one of: addDefaultSubnetValidatorTx, addNonDefaultSubnetValidatorTx, createSubnetTx")
	}
	if err != nil {
		return err
	}

	reply.Tx.Bytes, err = Codec.Marshal(genTx)
	return err
}

// Sign [unsigned] with [key]
func (service *Service) signAddDefaultSubnetValidatorTx(tx *addDefaultSubnetValidatorTx, key *crypto.PrivateKeySECP256K1R) (*addDefaultSubnetValidatorTx, error) {
	service.vm.Ctx.Log.Debug("platform.signAddDefaultSubnetValidatorTx called")

	// TODO: Should we check if tx is already signed?
	unsignedIntf := interface{}(&tx.UnsignedAddDefaultSubnetValidatorTx)
	unsignedTxBytes, err := Codec.Marshal(&unsignedIntf)
	if err != nil {
		return nil, fmt.Errorf("error serializing unsigned tx: %v", err)
	}

	sig, err := key.Sign(unsignedTxBytes)
	if err != nil {
		return nil, errors.New("error while signing")
	}
	if len(sig) != crypto.SECP256K1RSigLen {
		return nil, fmt.Errorf("expected signature to be length %d but was length %d", crypto.SECP256K1RSigLen, len(sig))
	}
	copy(tx.Sig[:], sig)

	return tx, nil
}

// Sign [unsigned] with [key]
func (service *Service) signAddDefaultSubnetDelegatorTx(tx *addDefaultSubnetDelegatorTx, key *crypto.PrivateKeySECP256K1R) (*addDefaultSubnetDelegatorTx, error) {
	service.vm.Ctx.Log.Debug("platform.signAddDefaultSubnetValidatorTx called")

	// TODO: Should we check if tx is already signed?
	unsignedIntf := interface{}(&tx.UnsignedAddDefaultSubnetDelegatorTx)
	unsignedTxBytes, err := Codec.Marshal(&unsignedIntf)
	if err != nil {
		return nil, fmt.Errorf("error serializing unsigned tx: %v", err)
	}

	sig, err := key.Sign(unsignedTxBytes)
	if err != nil {
		return nil, errors.New("error while signing")
	}
	if len(sig) != crypto.SECP256K1RSigLen {
		return nil, fmt.Errorf("expected signature to be length %d but was length %d", crypto.SECP256K1RSigLen, len(sig))
	}
	copy(tx.Sig[:], sig)

	return tx, nil
}

// Sign [xt] with [key]
func (service *Service) signCreateSubnetTx(tx *CreateSubnetTx, key *crypto.PrivateKeySECP256K1R) (*CreateSubnetTx, error) {
	service.vm.Ctx.Log.Debug("platform.signAddDefaultSubnetValidatorTx called")

	// TODO: Should we check if tx is already signed?
	unsignedIntf := interface{}(&tx.UnsignedCreateSubnetTx)
	unsignedTxBytes, err := Codec.Marshal(&unsignedIntf)
	if err != nil {
		return nil, fmt.Errorf("error serializing unsigned tx: %v", err)
	}

	sig, err := key.Sign(unsignedTxBytes)
	if err != nil {
		return nil, errors.New("error while signing")
	}
	if len(sig) != crypto.SECP256K1RSigLen {
		return nil, fmt.Errorf("expected signature to be length %d but was length %d", crypto.SECP256K1RSigLen, len(sig))
	}
	copy(tx.Sig[:], sig)

	return tx, nil
}

// Signs an unsigned or partially signed addNonDefaultSubnetValidatorTx with [key]
// If [key] is a control key for the subnet and there is an empty spot in tx.ControlSigs, signs there
// If [key] is a control key for the subnet and there is no empty spot in tx.ControlSigs, signs as payer
// If [key] is not a control key, sign as payer (account controlled by [key] pays the tx fee)
// Sorts tx.ControlSigs before returning
// Assumes each element of tx.ControlSigs is actually a signature, not just empty bytes
func (service *Service) signAddNonDefaultSubnetValidatorTx(tx *addNonDefaultSubnetValidatorTx, key *crypto.PrivateKeySECP256K1R) (*addNonDefaultSubnetValidatorTx, error) {
	service.vm.Ctx.Log.Debug("platform.signAddNonDefaultSubnetValidatorTx called")

	// Compute the byte repr. of the unsigned tx and the signature of [key] over it
	unsignedIntf := interface{}(&tx.UnsignedAddNonDefaultSubnetValidatorTx)
	unsignedTxBytes, err := Codec.Marshal(&unsignedIntf)
	if err != nil {
		return nil, fmt.Errorf("error serializing unsigned tx: %v", err)
	}
	sig, err := key.Sign(unsignedTxBytes)
	if err != nil {
		return nil, errors.New("error while signing")
	}
	if len(sig) != crypto.SECP256K1RSigLen {
		return nil, fmt.Errorf("expected signature to be length %d but was length %d", crypto.SECP256K1RSigLen, len(sig))
	}

	// Get information about the subnet
	subnet, err := service.vm.getSubnet(service.vm.DB, tx.SubnetID())
	if err != nil {
		return nil, fmt.Errorf("problem getting subnet information: %v", err)
	}

	// Find the location at which [key] should put its signature.
	// If [key] is a control key for this subnet and there is an empty spot in tx.ControlSigs, sign there
	// If [key] is a control key for this subnet and there is no empty spot in tx.ControlSigs, sign as payer
	// If [key] is not a control key, sign as payer (account controlled by [key] pays the tx fee)
	controlKeySet := ids.ShortSet{}
	controlKeySet.Add(subnet.ControlKeys...)
	isControlKey := controlKeySet.Contains(key.PublicKey().Address())

	payerSigEmpty := tx.PayerSig == [crypto.SECP256K1RSigLen]byte{} // true if no key has signed to pay the tx fee

	if isControlKey && len(tx.ControlSigs) != int(subnet.Threshold) { // Sign as controlSig
		tx.ControlSigs = append(tx.ControlSigs, [crypto.SECP256K1RSigLen]byte{})
		copy(tx.ControlSigs[len(tx.ControlSigs)-1][:], sig)
	} else if payerSigEmpty { // sign as payer
		copy(tx.PayerSig[:], sig)
	} else {
		return nil, errors.New("no place for key to sign")
	}

	crypto.SortSECP2561RSigs(tx.ControlSigs)

	return tx, nil
}

// IssueTxArgs are the arguments to IssueTx
type IssueTxArgs struct {
	// Tx being sent to the network
	Tx formatting.CB58 `json:"tx"`
}

// IssueTxResponse is the response from IssueTx
type IssueTxResponse struct {
	// ID of transaction being sent to network
	TxID ids.ID `json:"txID"`
}

// IssueTx issues the transaction [args.Tx] to the network
func (service *Service) IssueTx(_ *http.Request, args *IssueTxArgs, response *IssueTxResponse) error {
	genTx := genericTx{}
	if err := Codec.Unmarshal(args.Tx.Bytes, &genTx); err != nil {
		return err
	}

	switch tx := genTx.Tx.(type) {
	case TimedTx:
		if err := tx.initialize(service.vm); err != nil {
			return fmt.Errorf("error initializing tx: %s", err)
		}
		service.vm.unissuedEvents.Push(tx)
		defer service.vm.resetTimer()
		response.TxID = tx.ID()
		return nil
	case *CreateSubnetTx:
		if err := tx.initialize(service.vm); err != nil {
			return fmt.Errorf("error initializing tx: %s", err)
		}
		service.vm.unissuedDecisionTxs = append(service.vm.unissuedDecisionTxs, tx)
		defer service.vm.resetTimer()
		response.TxID = tx.ID
		return nil
	default:
		return errors.New("Could not parse given tx. Must be one of: addDefaultSubnetValidatorTx, addDefaultSubnetDelegatorTx, addNonDefaultSubnetValidatorTx, createSubnetTx")
	}
}

/*
 ******************************************************
 **************** Create a Subnet *********************
 ******************************************************
 */

// CreateSubnetArgs are the arguments to CreateSubnet
type CreateSubnetArgs struct {
	// The ID member of APISubnet is ignored
	APISubnet

	// Nonce of the account that pays the transaction fee
	PayerNonce json.Uint64 `json:"payerNonce"`
}

// CreateSubnetResponse is the response from a call to CreateSubnet
type CreateSubnetResponse struct {
	// Byte representation of the unsigned transaction to create a new subnet
	UnsignedTx formatting.CB58 `json:"unsignedTx"`
}

// CreateSubnet returns an unsigned transaction to create a new subnet.
// The unsigned transaction must be signed with the key of [args.Payer]
func (service *Service) CreateSubnet(_ *http.Request, args *CreateSubnetArgs, response *CreateSubnetResponse) error {
	service.vm.Ctx.Log.Debug("platform.createSubnet called")

	// Create the transaction
	tx := CreateSubnetTx{
		UnsignedCreateSubnetTx: UnsignedCreateSubnetTx{
			NetworkID:   service.vm.Ctx.NetworkID,
			Nonce:       uint64(args.PayerNonce),
			ControlKeys: args.ControlKeys,
			Threshold:   uint16(args.Threshold),
		},
		key:   nil,
		Sig:   [65]byte{},
		bytes: nil,
	}

	txBytes, err := Codec.Marshal(genericTx{Tx: &tx})
	if err != nil {
		return errCreatingTransaction
	}

	response.UnsignedTx.Bytes = txBytes
	return nil

}

/*
 ******************************************************
 ******** Create/get status of a blockchain ***********
 ******************************************************
 */

// CreateBlockchainArgs is the arguments for calling CreateBlockchain
type CreateBlockchainArgs struct {
	// ID of the VM the new blockchain is running
	VMID string `json:"vmID"`

	// IDs of the FXs the VM is running
	FxIDs []string `json:"fxIDs"`

	// Human-readable name for the new blockchain, not necessarily unique
	Name string `json:"name"`

	// To generate the byte representation of the genesis data for this blockchain,
	// a POST request with body [GenesisData] is made to the API method whose name is [Method], whose
	// endpoint is [Endpoint]. See Platform Chain documentation for more info and examples.
	Method      string      `json:"method"`
	Endpoint    string      `json:"endpoint"`
	GenesisData interface{} `json:"genesisData"`
}

// CreateGenesisReply is the reply from a call to CreateGenesis
type CreateGenesisReply struct {
	Bytes formatting.CB58 `json:"bytes"`
}

// CreateBlockchainReply is the reply from calling CreateBlockchain
type CreateBlockchainReply struct {
	BlockchainID ids.ID `json:"blockchainID"`
}

// CreateBlockchain issues a transaction to the network to create a new blockchain
func (service *Service) CreateBlockchain(_ *http.Request, args *CreateBlockchainArgs, reply *CreateBlockchainReply) error {
	vmID, err := service.vm.ChainManager.LookupVM(args.VMID)
	if err != nil {
		return fmt.Errorf("no VM with ID '%s' found", args.VMID)
	}

	fxIDs := []ids.ID(nil)
	for _, fxIDStr := range args.FxIDs {
		fxID, err := service.vm.ChainManager.LookupVM(fxIDStr)
		if err != nil {
			return fmt.Errorf("no FX with ID '%s' found", fxIDStr)
		}
		fxIDs = append(fxIDs, fxID)
	}

	genesisBytes := []byte(nil)
	if args.Method != "" {
		buf, err := json2.EncodeClientRequest(args.Method, args.GenesisData)
		if err != nil {
			return fmt.Errorf("problem building blockchain genesis state: %w", err)
		}

		writer := httptest.NewRecorder()
		service.vm.Ctx.HTTP.Call(
			/*writer=*/ writer,
			/*method=*/ "POST",
			/*base=*/ args.VMID,
			/*endpoint=*/ args.Endpoint,
			/*body=*/ bytes.NewBuffer(buf),
			/*headers=*/ map[string]string{
				"Content-Type": "application/json",
			},
		)

		result := CreateGenesisReply{}
		if err := json2.DecodeClientResponse(writer.Body, &result); err != nil {
			return fmt.Errorf("problem building blockchain genesis state: %w", err)
		}
		genesisBytes = result.Bytes.Bytes
	} else if args.GenesisData != nil {
		return errNoMethodWithGenesis
	}

	// TODO: Should use the key store to sign this transaction.
	// TODO: Nonce shouldn't always be 0
	tx, err := service.vm.newCreateChainTx(0, genesisBytes, vmID, fxIDs, args.Name, service.vm.Ctx.NetworkID, key)
	if err != nil {
		return fmt.Errorf("problem creating transaction: %w", err)
	}

	// Add this tx to the set of unissued txs
	service.vm.unissuedDecisionTxs = append(service.vm.unissuedDecisionTxs, tx)
	service.vm.resetTimer()

	reply.BlockchainID = tx.ID()

	return nil
}

// GetBlockchainStatusArgs is the arguments for calling GetBlockchainStatus
// [BlockchainID] is the blockchain to get the status of.
type GetBlockchainStatusArgs struct {
	BlockchainID string `json:"blockchainID"`
}

// GetBlockchainStatusReply is the reply from calling GetBlockchainStatus
// [Status] is the blockchain's status.
type GetBlockchainStatusReply struct {
	Status Status `json:"status"`
}

// GetBlockchainStatus gets the status of a blockchain with the ID [args.BlockchainID].
func (service *Service) GetBlockchainStatus(_ *http.Request, args *GetBlockchainStatusArgs, reply *GetBlockchainStatusReply) error {
	_, err := service.vm.ChainManager.Lookup(args.BlockchainID)
	if err == nil {
		reply.Status = Validating
		return nil
	}

	bID, err := ids.FromString(args.BlockchainID)
	if err != nil {
		return fmt.Errorf("problem parsing blockchainID '%s': %w", args.BlockchainID, err)
	}

	lastAcceptedID := service.vm.LastAccepted()
	if exists, err := service.chainExists(lastAcceptedID, bID); err != nil {
		return fmt.Errorf("problem looking up blockchain: %w", err)
	} else if exists {
		reply.Status = Created
		return nil
	}

	preferred := service.vm.Preferred()
	if exists, err := service.chainExists(preferred, bID); err != nil {
		return fmt.Errorf("problem looking up blockchain: %w", err)
	} else if exists {
		reply.Status = Preferred
		return nil
	}

	return nil
}

func (service *Service) chainExists(blockID ids.ID, chainID ids.ID) (bool, error) {
	blockIntf, err := service.vm.getBlock(blockID)
	if err != nil {
		return false, err
	}

	block, ok := blockIntf.(decision)
	if !ok {
		block, ok = blockIntf.Parent().(decision)
		if !ok {
			return false, errMissingDecisionBlock
		}
	}
	db := block.onAccept()

	chains, err := service.vm.getChains(db)
	for _, chain := range chains {
		if chain.ID().Equals(chainID) {
			return true, nil
		}
	}

	return false, nil
}
