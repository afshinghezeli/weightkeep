package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/oms"
)

func newKeyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage signing keys"}
	var out string
	gen := &cobra.Command{
		Use:   "generate",
		Short: "Create a P-256 key pair for signing manifests",
		Long: `generate writes a new ECDSA P-256 key pair: <name>.key (private, mode 0600)
and <name>.pub. The key signs OpenSSF Model Signing bundles with
'weightkeep manifest sign'. Keep the private key somewhere safe; share the
.pub file with whoever verifies.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := oms.GenerateKey()
			if err != nil {
				return err
			}
			priv, err := oms.PrivateKeyPEM(key)
			if err != nil {
				return err
			}
			pub, err := oms.PublicKeyPEM(&key.PublicKey)
			if err != nil {
				return err
			}
			for _, p := range []string{out + ".key", out + ".pub"} {
				if _, err := os.Stat(p); err == nil {
					return fmt.Errorf("%s exists; not overwriting a key", p)
				}
			}
			if err := os.WriteFile(out+".key", priv, 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(out+".pub", pub, 0o644); err != nil {
				return err
			}
			hint, _ := oms.KeyHint(&key.PublicKey)
			cmd.PrintErrf("wrote %s.key and %s.pub (fingerprint %s)\n", out, out, hint[:16])
			return nil
		},
	}
	gen.Flags().StringVar(&out, "out", "weightkeep-signing", "file name prefix")
	cmd.AddCommand(gen)
	return cmd
}

func newManifestCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Show, sign and verify revision manifests",
		Long: `A manifest lists every file of a kept revision with its size and SHA-256.

'sign' wraps it in an OpenSSF Model Signing (OMS) v1.0 bundle, which the
reference tool verifies too:

  weightkeep export REPO --to ./model --link copy
  model_signing verify key --signature model.sig --public_key key.pub ./model`,
	}
	cmd.AddCommand(newManifestShowCommand(d), newManifestSignCommand(d), newManifestVerifyCommand(d))
	return cmd
}

func loadRevision(cmd *cobra.Command, a *app, arg string) (*manifest.Manifest, error) {
	sel, err := selectorFromArg(cmd.Context(), a, withDefaultRev(arg))
	if err != nil {
		return nil, err
	}
	return manifest.Load(cmd.Context(), a.store, sel.Repo, sel.Commit)
}

func newManifestShowCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show REPO[@REVISION]",
		Short: "Print a kept revision's manifest as canonical JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			m, err := loadRevision(cmd, a, args[0])
			if err != nil {
				return err
			}
			b, err := m.Canonical()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
}

func newManifestSignCommand(d deps) *cobra.Command {
	var keyPath, out, name string
	cmd := &cobra.Command{
		Use:   "sign REPO[@REVISION] --key FILE",
		Short: "Sign a kept revision as an OpenSSF Model Signing bundle",
		Long: `sign writes an OMS v1.0 bundle for a kept revision, signed with an ECDSA key
(P-256, P-384 or P-521; see 'weightkeep key generate'). It signs the SHA-256
of every file except .git* files, which OMS always leaves out.`,
		Example: `  weightkeep manifest sign HuggingFaceTB/SmolLM2-135M --key weightkeep-signing.key --output model.sig`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyPath == "" {
				return errors.New("--key is required")
			}
			raw, err := os.ReadFile(keyPath)
			if err != nil {
				return err
			}
			key, err := oms.ParsePrivateKey(raw)
			if err != nil {
				return err
			}
			a, err := d.openApp(cmd.Context())
			if err != nil {
				return err
			}
			defer a.Close()
			m, err := loadRevision(cmd, a, args[0])
			if err != nil {
				return err
			}
			if name == "" {
				name = m.Repo.Name()
			}
			st, err := oms.FromManifest(m, name)
			if err != nil {
				return err
			}
			bundle, err := oms.Sign(st, key)
			if err != nil {
				return err
			}
			if out == "-" {
				_, err = cmd.OutOrStdout().Write(append(bundle, '\n'))
				return err
			}
			if err := os.WriteFile(out, append(bundle, '\n'), 0o644); err != nil { //nolint:gosec // G703: --output is the user's choice
				return err
			}
			cmd.PrintErrf("signed %s@%s (%d files) into %s\n", m.Repo, m.Commit[:12], len(st.Predicate.Resources), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", "", "private key (PEM)")
	cmd.Flags().StringVar(&out, "output", "model.sig", "where to write the bundle (- for stdout)")
	cmd.Flags().StringVar(&name, "name", "", "subject name in the bundle (default: the repo name)")
	return cmd
}

func newManifestVerifyCommand(d deps) *cobra.Command {
	var sigPath, pubPath, dir string
	cmd := &cobra.Command{
		Use:   "verify [REPO[@REVISION]] --signature FILE --public-key FILE",
		Short: "Check an OMS bundle against a kept revision or a directory",
		Long: `verify checks the bundle's signature with the public key, then compares the
signed file hashes with a kept revision (by name) or with the files in --dir,
for example a directory written by 'weightkeep export --to'. Every signed file
must match, and every file outside .git* must be signed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sigPath == "" || pubPath == "" {
				return errors.New("--signature and --public-key are required")
			}
			if (len(args) == 1) == (dir != "") {
				return errors.New("give either a kept revision or --dir")
			}
			bundle, err := os.ReadFile(sigPath)
			if err != nil {
				return err
			}
			rawPub, err := os.ReadFile(pubPath)
			if err != nil {
				return err
			}
			pub, err := oms.ParsePublicKey(rawPub)
			if err != nil {
				return err
			}
			st, err := oms.Verify(bundle, pub)
			if err != nil {
				return fmt.Errorf("signature: %w", err)
			}
			var mm []oms.Mismatch
			what := dir
			if dir != "" {
				rel := ""
				if abs, err := filepath.Abs(sigPath); err == nil {
					if absDir, err := filepath.Abs(dir); err == nil {
						if r, err := filepath.Rel(absDir, abs); err == nil && !filepath.IsAbs(r) && r[0] != '.' {
							rel = filepath.ToSlash(r)
						}
					}
				}
				if mm, err = oms.VerifyDir(st, dir, rel); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return fmt.Errorf("--dir %s: %w", dir, err)
					}
					return err
				}
			} else {
				a, err := d.openApp(cmd.Context())
				if err != nil {
					return err
				}
				defer a.Close()
				m, err := loadRevision(cmd, a, args[0])
				if err != nil {
					return err
				}
				mm = oms.Compare(st, m)
				what = m.Repo.String() + "@" + m.Commit[:12]
			}
			for _, x := range mm {
				fmt.Fprintf(cmd.OutOrStdout(), "MISMATCH  %s: %s\n", x.Path, x.Reason)
			}
			if len(mm) > 0 {
				return fmt.Errorf("%d file(s) don't match the signed manifest", len(mm))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ok  %s matches the signed manifest (%d files)\n", what, len(st.Predicate.Resources))
			return nil
		},
	}
	cmd.Flags().StringVar(&sigPath, "signature", "", "the OMS bundle")
	cmd.Flags().StringVar(&pubPath, "public-key", "", "the signer's public key (PEM)")
	cmd.Flags().StringVar(&dir, "dir", "", "verify the files in this directory instead of a kept revision")
	return cmd
}
