<p align="center">
  <img src="logo/transparent/nasbuster-256.png" width="128" alt="Logo de NasBuster">
</p>

# CrossWax NasBuster

> Un NAS léger et moderne pour Raspberry Pi : vrai RAID, snapshots, Docker, mise en route guidée.

[English](README.md) · **Français**

**Statut : bêta publique (v0.1).** Testez-le sur un Pi qui ne porte pas votre unique copie de quoi que ce soit.

NasBuster se situe entre OpenMediaVault et TrueNAS :

- **Il s'installe sur un système standard** (Raspberry Pi OS Lite 64 bits, Debian), comme OpenMediaVault. Pas d'appliance verrouillée.
- **L'intégrité des données de TrueNAS** : sommes de contrôle, vérifications (scrubs) planifiées, miroirs qui se réparent seuls.
- **Une empreinte minimale** : un seul service Go d'environ 20 Mo de RAM, ni Python, ni PHP, ni Node sur le Pi.
- **Une mise en route pas à pas** depuis un navigateur, en anglais ou en français.

## Fonctions

- **Stockage** : Btrfs sur un disque, en miroir et en RAID10 ; parité par mdadm RAID5/6 sous Btrfs.
- **Pannes de disque** : montage dégradé, remplacement guidé, retour automatique à la pleine redondance.
- **Perte de la carte SD** : les pools existants sont importés après une réinstallation, et la configuration est sauvegardée sur chaque pool, d'où elle se restaure.
- **Partages** : SMB avec des droits par utilisateur, snapshots horaires visibles dans les *Versions précédentes* de Windows, Time Machine, corbeille, découverte réseau.
- **Import** : copie des données d'un disque d'un ancien NAS ou d'une clé USB, en lecture seule.
- **Applications** : Docker installé à la demande, avec ses données sur un pool, applications Compose, petit catalogue (Portainer CE, Uptime Kuma).
- **Alertes** : santé des pools, alimentation, température et SMART, envoyées une fois par changement, par ntfy ou webhook.
- **HTTPS** avec un certificat auto-signé dont l'empreinte est affichée.

## Prérequis

- Raspberry Pi 4 ou 5 avec un système 64 bits : Raspberry Pi OS Lite 64 bits ou Debian 12 ou 13 (testé sur Debian 13 « Trixie »). Les machines amd64 fonctionnent aussi.
- Des disques de données en USB ou en NVMe. La carte SD ne porte que le système. Sur un Pi 4, branchez les disques USB derrière un hub alimenté.
- Le port 443 libre : NasBuster y sert son interface web.

## Matériel conseillé

Un miroir de deux disques, compact et silencieux (1 To utile). Les liens mènent à Amazon.fr.

| Élément | Modèle conseillé | Ce qu'il faut regarder |
|---|---|---|
| Raspberry Pi 5, 8 Go | [Raspberry Pi 5, 8 Go](https://link.amazon/B0j62lUhG) | 4 Go suffisent à NasBuster seul ; 8 Go laissent de la place aux applications Docker. |
| Alimentation officielle de 27 W (USB-C) | [Raspberry Pi 27 W USB-C](https://link.amazon/B02qCPOOx) | Elle annonce 5 A en USB-PD ; avec une alimentation plus faible, le Pi bride ses ports USB, et deux SSD risquent de manquer de courant. |
| HAT M.2 NVMe double pour Pi 5 | [Geekworm X1004](https://link.amazon/B02NGxn3v) | Deux emplacements sur l'unique ligne PCIe du Pi, partagée par les deux disques (environ 400 Mo/s au total, plus que l'Ethernet gigabit). Vérifiez que votre SSD figure dans la liste de compatibilité du fabricant du HAT. |
| 2 × SSD NVMe de 1 To | [Lexar NM610 Pro, 1 To](https://link.amazon/B0aGh7h9u) (× 2) | Deux disques identiques pour un miroir Btrfs : si l'un meurt, rien n'est perdu. |
| Boîtier ventilé | [Boîtier aluminium ElectroCookie](https://link.amazon/B00yQkwFB) | Gardez un refroidissement actif : un Pi 5 trop chaud ralentit. Vérifiez que le boîtier accueille le Pi 5 avec le HAT. |
| Carte microSD de 32 Go | [Lexar microSDHC, 32 Go](https://link.amazon/B03NvZonO) | Elle ne porte que le système ; vos données n'y vont jamais. Une marque connue compte plus que la vitesse. |

*Ce sont des liens d'affiliation Amazon : en tant que Partenaire Amazon, le projet touche une petite commission sur les achats remplissant les conditions requises, sans surcoût pour vous. Elle aide à payer le matériel de test.*

## Installation

Les paquets viennent d'un dépôt APT signé : <https://chrisboardoo.github.io/NASBuster/>.

**1. Installer le système**

Écrivez Raspberry Pi OS Lite (64 bits) sur la carte microSD avec [Raspberry Pi Imager](https://www.raspberrypi.com/software/). Dans ses réglages, choisissez un utilisateur et un mot de passe, et activez SSH. Démarrez le Pi, puis connectez-vous en SSH.

**2. Récupérer la clé de signature et la vérifier**

```bash
sudo curl -fsSL -o /usr/share/keyrings/nasbuster-archive-keyring.asc https://chrisboardoo.github.io/NASBuster/nasbuster-archive-keyring.asc
gpg --show-keys /usr/share/keyrings/nasbuster-archive-keyring.asc
```

L'empreinte doit être exactement :

```text
6E4A B0CB FCC4 E45D EC76  A4B4 5282 C4BC 9717 0E72
```

Si elle diffère, arrêtez-vous : quelqu'un sert peut-être une fausse clé. Ensuite, le paquet tient lui-même cette clé à jour.

**3. Ajouter le dépôt**

```bash
sudo tee /etc/apt/sources.list.d/nasbuster.sources >/dev/null <<'EOF'
Types: deb
URIs: https://chrisboardoo.github.io/NASBuster
Suites: stable
Components: main
Signed-By: /usr/share/keyrings/nasbuster-archive-keyring.asc
EOF
```

**4. Installer**

```bash
sudo apt update
sudo apt install nasbuster
```

**5. Mettre en route**

Ouvrez `https://<votre-pi>/` dans un navigateur. Le certificat est auto-signé : le navigateur avertit une fois. Pour être sûr d'arriver sur votre Pi, comparez l'empreinte qu'il affiche avec :

```bash
sudo nasbusterd -print-tls-fingerprint
```

La mise en route vous guide ensuite : vous prouvez que la machine est à vous avec le compte de connexion du Pi (membre du groupe `sudo`), vous créez l'administrateur du NAS, puis un pool, un utilisateur et un premier partage.

## Mise à jour

```bash
sudo apt update && sudo apt upgrade
```

## Désinstallation

```bash
sudo apt remove nasbuster    # arrête NasBuster, garde tout
sudo apt purge nasbuster     # efface aussi ses réglages et ses comptes web
```

Aucune de ces commandes ne touche à vos pools, à vos partages, à leurs données ni aux comptes des utilisateurs du NAS.

## Compiler depuis les sources

Il faut Go et pnpm. Sous Linux :

```bash
make web package-arm64    # dist/nasbuster_<version>_arm64.deb
make test
```

Chaque étiquette de version de ce dépôt contient la source exacte des paquets publiés avec elle.

## Soutenir le projet

NasBuster est gratuit, et rien n'y est bloqué. S'il vous est utile, vous pouvez offrir un café au projet sur [Ko-fi](https://ko-fi.com/crosswax) : cela paie le matériel de test.

## Licence

[GNU Affero General Public License v3.0](LICENSE) ou ultérieure.

Le logo de NasBuster (le fantôme) n'est pas couvert par l'AGPL : il a sa propre licence, dans [logo/LICENSE.txt](logo/LICENSE.txt). En bref, utilisez-le librement pour parler de NasBuster, mais un fork prend son propre logo.
