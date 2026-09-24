# Upstream maintenance

Upstream repository:
https://github.com/AlexanderMckain/Silo-Redact.git

Upstream tracking branch: `upstream-main`

OneBoard customization branch: `main`

Baseline commit: `775d90da287d372f560b56b5eefd9f574741c39d`

`upstream-main` is the pristine upstream reference. OneBoard-specific branding
and other customizations belong only on `main`.

For a future upstream update:

```powershell
git fetch upstream
git log --oneline upstream-main..upstream/main
git switch upstream-main
git merge --ff-only upstream/main
git switch main
git merge upstream-main
```

Review the upstream changes before updating `upstream-main`. Resolve only the
OneBoard customization conflicts on `main`, then rebuild and test the Windows
application. Cherry-pick selected upstream commits instead of merging only when
there is a clear maintenance reason to do so.

Do not add OneBoard commits to `upstream-main`, squash away upstream history, or
force-push either primary branch.
