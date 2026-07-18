# Granting a headless environment read access to private docs

A CI job or a remote coding agent (Claude Code on the web, a cloud dev
environment, a build box) clones the repo with no SSH key, so `private` docs
appear as age ciphertext (`-----BEGIN AGE ENCRYPTED FILE-----` blocks) and
`doctier unlock` / `doctier cat` fail. That is the tier working as designed —
the stock CI recipes ([GitHub Actions](ci/github-actions.yml),
[GitLab CI](ci/gitlab-ci.yml)) need no key at all, because `doctier check` and
`doctier gc` never decrypt.

Sometimes, though, you *want* such an environment to read private docs — an
agent that needs the product strategy for context, a job that renders an
internal report. Access is granted deliberately, in three steps. The
environment **cannot self-serve**: a key it adds for itself can never decrypt
blobs that were encrypted before it became a recipient, so the grant must run
on a machine where an existing recipient's key already lives.

## The trade-off, stated plainly

Granting an environment a key makes every private doc readable to that
environment **and to anyone who can read its secrets** — repo and org admins
on the CI side, the platform operator on the agent side. That is exactly the
exposure the `private` tier otherwise prevents, so treat each grant as a
security decision:

- Use a **dedicated key per environment**, never a personal one — revoking
  then costs nothing but that environment's access.
- Grant the narrowest environment that needs it, and revoke when it is
  retired.
- Revocation is forward-only: a leaked key still decrypts every version
  already in git history.

## 1. Generate a dedicated key

Anywhere — your machine is fine; the private half moves into the environment's
secret store in step 3:

```bash
ssh-keygen -t ed25519 -f agent_key -N "" -C "doctier: <environment name>"
```

Passphrase-less (`-N ""`) is required: nobody is there to type a passphrase in
a headless run. The `-C` comment names the environment — it is what you will
look for when revoking.

## 2. Grant it — from a machine that already has access

On a clone where a current recipient's key is available (**not** in the
headless environment — `grant` re-encrypts, and re-encrypting starts by
decrypting, which needs a key that already has access):

```bash
doctier grant "$(cat agent_key.pub)"
git add -A && git commit -m "grant doctier access to <environment>"
git push
```

`grant` appends the key to the recipients file and re-encrypts every private
doc so the new key can read them.

## 3. Provision the key in the environment

Store the **private** half (`agent_key`) as a secret and expose it as
`DOCTIER_SSH_KEY`. The variable accepts either a file path or the key material
itself — secrets usually arrive as values, so inline is the common case:

```yaml
# GitHub Actions
env:
  DOCTIER_SSH_KEY: ${{ secrets.DOCTIER_AGENT_KEY }}
```

For a remote coding agent, set `DOCTIER_SSH_KEY` in the environment's
secrets / environment-variable settings the same way. Then delete the local
copy of `agent_key`.

In the environment, once its clone includes the grant commit:

```bash
doctier unlock          # decrypt all private docs into the working tree
doctier cat <path>      # or: print one doc to stdout, nothing written to disk
```

Prefer `doctier cat` when the environment only needs to *read* — it leaves no
plaintext on disk. `doctier unlock` materializes plaintext in the working
tree, which the clean filter re-encrypts on commit as usual.

## Revoking

When the environment is retired, or its secret may have leaked:

```bash
# 1. delete the environment's line from .doctier/recipients.txt
doctier grant          # no argument: re-encrypt to the remaining recipients
git add -A && git commit -m "revoke doctier access for <environment>"
git push
```

Forward-only, again: the removed key still decrypts everything already in git
history. If the key actually leaked, treat historical private docs as exposed
and rotate their *contents*, not just the key.

## FAQ: my agent pasted me an `AGE ENCRYPTED FILE` block

That is how this gap usually announces itself: the agent opened a private doc,
found ciphertext, and relayed it verbatim. Nothing is broken — the environment
simply has no granted key. Decide whether it should have one (this guide), or
leave it keyless: that is a supported state, and `doctier agents` already
omits unreadable private docs from the context block it emits, so a keyless
agent is not pointed at ciphertext in the first place.
