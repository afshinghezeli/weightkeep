# Reference bundle

`model.sig` was produced by the OMS reference implementation (`pip install model-signing==1.1.1`) with a
throwaway P-256 key, for testing that weightkeep verifies bundles it didn't make:

```sh
openssl ecparam -name prime256v1 -genkey -noout -out key.pem
openssl ec -in key.pem -pubout -out pub.pem
model_signing sign key --private_key key.pem --signature model.sig model
```

The private key was discarded.
