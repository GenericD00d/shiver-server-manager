# Member guide: self-signed servers

If your community's server uses a self-signed certificate, your browser and apps will warn that the connection isn't private until you install the server's **root certificate** once on each device. After that, the warning is gone for good (the root lasts about ten years).

Servers using Let's Encrypt don't need any of this.

## 1. Get the root certificate

Your admin will share a link like `http://chat.example.com/sharkord-root.crt` and, separately (chat, email), its **SHA-256 fingerprint**. The download isn't encrypted, so check the fingerprint before trusting the file:

- Windows, in PowerShell: `(New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 "$PWD\sharkord-root.crt").GetCertHashString('SHA256')` (it prints the same digits without colons).
- macOS and Linux: `openssl x509 -in sharkord-root.crt -noout -fingerprint -sha256`.
- iPhone and Android show the SHA-256 fingerprint in the certificate's details before you install it.

(Hashing the file itself, with `sha256sum` or `certutil -hashfile`, gives a different number: that's the file, not the certificate.)

The fingerprint must match what the admin sent you exactly. If it doesn't, don't install it, and tell the admin.

## 2. Install it

### Windows

Double-click the file, choose **Install Certificate**, then **Local Machine**, and place it in **Trusted Root Certification Authorities**.

### macOS

Open it in **Keychain Access** and add it to the **System** keychain. Then open it there and set **When using this certificate** to **Always Trust**.

### iPhone and iPad

Open the file and install the profile in Settings. Then turn on full trust for it in **Settings > General > About > Certificate Trust Settings**.

### Android

**Settings > Security > Encryption & credentials > Install a certificate > CA certificate** (names vary by phone). Browsers then trust it; most other apps ignore certificates you add.

### Linux

Copy it to `/usr/local/share/ca-certificates/` as a `.crt` file and run `sudo update-ca-certificates`.

Chrome-based browsers and the Sharkord desktop app keep their own list, so also run (from the `libnss3-tools` package):

    certutil -d sql:$HOME/.pki/nssdb -A -t C,, -n sharkord -i sharkord-root.crt

### Firefox (any system)

Firefox has its own list: **Settings > Privacy & Security > Certificates > View Certificates > Authorities > Import**, choose the file, and tick **Trust this CA to identify websites**.

## Shiver apps

The Shiver apps for Android and Windows are on [Shiver's releases page](https://github.com/GenericD00d/Shiver/releases). The Windows installer isn't code-signed, so SmartScreen warns on first run. Shiver takes over `sharkord://` links from Sharkord's own desktop app, so install one or the other.
