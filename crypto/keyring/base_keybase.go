package keyring

import (
	"github.com/pkg/errors"

	tmcrypto "github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/crypto/secp256k1"

	"github.com/cosmos/cosmos-sdk/crypto"
	"github.com/cosmos/cosmos-sdk/crypto/keys/hd"
	"github.com/cosmos/cosmos-sdk/types"
	bip39 "github.com/cosmos/go-bip39"
)

type (
	// baseKeybase is an auxiliary type that groups Keybase storage agnostic features
	// together.
	baseKeybase struct {
		options kbOptions
	}

	keyWriter interface {
		writeLocalKeyer
		infoWriter
	}

	writeLocalKeyer interface {
		writeLocalKey(name string, priv tmcrypto.PrivKey, passphrase string, algo SigningAlgo) Info
	}

	infoWriter interface {
		writeInfo(name string, info Info)
	}
)

// newBaseKeybase generates the base keybase with defaulting to tendermint SECP256K1 key type
func newBaseKeybase(optionsFns ...KeybaseOption) baseKeybase {
	// Default options for keybase
	options := kbOptions{
		keygenFunc:           StdPrivKeyGen,
		deriveFunc:           StdDeriveKey,
		supportedAlgos:       []SigningAlgo{Secp256k1},
		supportedAlgosLedger: []SigningAlgo{Secp256k1},
	}

	for _, optionFn := range optionsFns {
		optionFn(&options)
	}

	return baseKeybase{options: options}
}

// StdPrivKeyGen is the default PrivKeyGen function in the keybase.
// For now, it only supports Secp256k1
func StdPrivKeyGen(bz []byte, algo SigningAlgo) (tmcrypto.PrivKey, error) {
	if algo == Secp256k1 {
		return SecpPrivKeyGen(bz), nil
	}
	return nil, ErrUnsupportedSigningAlgo
}

// SecpPrivKeyGen generates a secp256k1 private key from the given bytes
func SecpPrivKeyGen(bz []byte) tmcrypto.PrivKey {
	var bzArr [32]byte
	copy(bzArr[:], bz)
	return secp256k1.PrivKeySecp256k1(bzArr)
}

// CreateAccount creates an account Info object.
func (kb baseKeybase) CreateAccount(
	keyWriter keyWriter, name, mnemonic, bip39Passphrase, encryptPasswd, hdPath string, algo SigningAlgo,
) (Info, error) {

	// create master key and derive first key for keyring
	derivedPriv, err := kb.options.deriveFunc(mnemonic, bip39Passphrase, hdPath, algo)
	if err != nil {
		return nil, err
	}

	privKey, err := kb.options.keygenFunc(derivedPriv, algo)
	if err != nil {
		return nil, err
	}

	var info Info

	if encryptPasswd != "" {
		info = keyWriter.writeLocalKey(name, privKey, encryptPasswd, algo)
	} else {
		info = kb.writeOfflineKey(keyWriter, name, privKey.PubKey(), algo)
	}

	return info, nil
}

// CreateLedger creates a new reference to a Ledger key pair. It returns a public
// key and a derivation path. It returns an error if the device could not be queried.
func (kb baseKeybase) CreateLedger(
	w infoWriter, name string, algo SigningAlgo, hrp string, account, index uint32,
) (Info, error) {

	if !IsSupportedAlgorithm(kb.SupportedAlgosLedger(), algo) {
		return nil, ErrUnsupportedSigningAlgo
	}

	coinType := types.GetConfig().GetCoinType()
	hdPath := hd.NewFundraiserParams(account, coinType, index)

	priv, _, err := crypto.NewPrivKeyLedgerSecp256k1(*hdPath, hrp)
	if err != nil {
		return nil, err
	}

	return kb.writeLedgerKey(w, name, priv.PubKey(), *hdPath, algo), nil
}

// CreateMnemonic generates a new key with the given algorithm and language pair.
func (kb baseKeybase) CreateMnemonic(
	keyWriter keyWriter, name string, language Language, passwd string, algo SigningAlgo,
) (info Info, mnemonic string, err error) {

	if language != English {
		return nil, "", ErrUnsupportedLanguage
	}

	if !IsSupportedAlgorithm(kb.SupportedAlgos(), algo) {
		return nil, "", ErrUnsupportedSigningAlgo
	}

	// Default number of words (24): This generates a mnemonic directly from the
	// number of words by reading system entropy.
	entropy, err := bip39.NewEntropy(defaultEntropySize)
	if err != nil {
		return nil, "", err
	}

	mnemonic, err = bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, "", err
	}

	info, err = kb.CreateAccount(keyWriter, name, mnemonic, DefaultBIP39Passphrase, passwd, types.GetConfig().GetFullFundraiserPath(), algo)
	if err != nil {
		return nil, "", err
	}

	return info, mnemonic, err
}

func (kb baseKeybase) writeLedgerKey(w infoWriter, name string, pub tmcrypto.PubKey, path hd.BIP44Params, algo SigningAlgo) Info {
	info := newLedgerInfo(name, pub, path, algo)
	w.writeInfo(name, info)
	return info
}

func (kb baseKeybase) writeOfflineKey(w infoWriter, name string, pub tmcrypto.PubKey, algo SigningAlgo) Info {
	info := newOfflineInfo(name, pub, algo)
	w.writeInfo(name, info)
	return info
}

func (kb baseKeybase) writeMultisigKey(w infoWriter, name string, pub tmcrypto.PubKey) Info {
	info := NewMultiInfo(name, pub)
	w.writeInfo(name, info)
	return info
}

// StdDeriveKey is the default DeriveKey function in the keybase.
// For now, it only supports Secp256k1
func StdDeriveKey(mnemonic string, bip39Passphrase, hdPath string, algo SigningAlgo) ([]byte, error) {
	if algo == Secp256k1 {
		return SecpDeriveKey(mnemonic, bip39Passphrase, hdPath)
	}
	return nil, ErrUnsupportedSigningAlgo
}

// SecpDeriveKey derives and returns the secp256k1 private key for the given seed and HD path.
func SecpDeriveKey(mnemonic string, bip39Passphrase, hdPath string) ([]byte, error) {
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, bip39Passphrase)
	if err != nil {
		return nil, err
	}

	masterPriv, ch := hd.ComputeMastersFromSeed(seed)
	if len(hdPath) == 0 {
		return masterPriv[:], nil
	}
	derivedKey, err := hd.DerivePrivateKeyForPath(masterPriv, ch, hdPath)
	return derivedKey[:], err
}

// CreateHDPath returns BIP 44 object from account and index parameters.
func CreateHDPath(account uint32, index uint32) *hd.BIP44Params {
	return hd.NewFundraiserParams(account, types.GetConfig().GetCoinType(), index)
}

// SupportedAlgos returns a list of supported signing algorithms.
func (kb baseKeybase) SupportedAlgos() []SigningAlgo {
	return kb.options.supportedAlgos
}

// SupportedAlgosLedger returns a list of supported ledger signing algorithms.
func (kb baseKeybase) SupportedAlgosLedger() []SigningAlgo {
	return kb.options.supportedAlgosLedger
}

// SignWithLedger signs a binary message with the ledger device referenced by an Info object
// and returns the signed bytes and the public key. It returns an error if the device could
// not be queried or it returned an error.
func SignWithLedger(info Info, msg []byte) (sig []byte, pub tmcrypto.PubKey, err error) {
	switch info.(type) {
	case *ledgerInfo, ledgerInfo:
	default:
		return nil, nil, errors.New("not a ledger object")
	}
	path, err := info.GetPath()
	if err != nil {
		return
	}

	priv, err := crypto.NewPrivKeyLedgerSecp256k1Unsafe(*path)
	if err != nil {
		return
	}

	sig, err = priv.Sign(msg)
	if err != nil {
		return nil, nil, err
	}

	return sig, priv.PubKey(), nil
}
