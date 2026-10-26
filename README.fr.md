<div align="center">

![new-api](/web/public/logo.png)

# New API

**Une passerelle IA dont la facturation et le contrôle d'accès sont conçus pour la revente**

<p align="center">
  <a href="./README.zh_CN.md">简体中文</a> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <a href="./README.md">English</a> |
  <strong>Français</strong> |
  <a href="./README.ja.md">日本語</a>
</p>

<p align="center">
  <strong>Fork de <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a></strong> ·
  <a href="#what-this-fork-adds">Ce que ce fork ajoute</a> ·
  <a href="#quick-start">Démarrage rapide</a> ·
  <a href="#upstream">Projet amont</a>
</p>

</div>

---

## Ce qu'est ce projet

Ce projet est un fork de [new-api](https://github.com/QuantumNous/new-api), une passerelle IA auto-hébergée qui agrège plus de 40 fournisseurs de modèles derrière une API compatible OpenAI.

L'amont couvre déjà le routage des modèles, le contrôle d'accès, les journaux d'utilisation et la facturation par portefeuille. **Ce fork conserve tout cela et ajoute ce qu'il faut pour revendre l'accès** : des plans d'abonnement, un solde d'aide sociale, un vrai système de codes de reprise, et les garanties comptables qui vont avec.

Tout ce qui suit est ce que ce fork ajoute ou modifie. Pour le reste — protocoles, canaux, déploiement, système de plugins, bases de la console — voir la section <a href="#upstream">projet amont</a> ; le comportement est identique.

---

<a id="what-this-fork-adds"></a>

## Ce que ce fork ajoute

### 1. Plans d'abonnement

L'amont n'a qu'un portefeuille. Ce fork ajoute une seconde source de financement avec son propre cycle de vie.

| Capacité | Détail |
| --- | --- |
| Gestion des plans | Créer / modifier / désactiver des plans ; prix, durée, quota total, groupes de montée et de descente en catégorie |
| Réinitialisation du quota | Remise à zéro du quota utilisé selon un cycle : jamais / quotidien / hebdomadaire / mensuel / secondes personnalisées |
| Groupes d'utilisation | Restreindre un plan à certains groupes de routage ; vide signifie tous les groupes |
| Limite de concurrence | Plafonne les requêtes simultanées par abonnement (0 = illimité) |
| Dépassement par portefeuille | Choisir si un plan peut basculer sur le solde du portefeuille quand son quota est épuisé |
| Limite d'achat | Nombre maximum d'achats d'un même plan par utilisateur |
| Voies d'attribution | Paiement par solde, Stripe, Creem, Epay, WaffoPancake, code de reprise, ou attribution directe par un administrateur |
| Opérations d'administration | Lier, invalider, supprimer, réinitialiser par plan ou par utilisateur |

Console : **Subscriptions** — une seule surface pour les administrateurs (création/édition de plans, liaison, invalidation, réinitialisation) et pour les utilisateurs (leurs abonnements, achat, stratégie de facturation).

### 2. Solde d'aide sociale

Un solde distinct du portefeuille, accordé par un administrateur.

| Capacité | Détail |
| --- | --- |
| Attribution par l'administrateur | Définir le solde d'aide sociale de n'importe quel utilisateur et son expiration, depuis la boîte de dialogue d'édition utilisateur |
| Ordre de consommation | L'aide sociale est dépensée avant le portefeuille et l'abonnement quand elle peut couvrir la requête |
| Expiration | Expiration absolue optionnelle ; un solde expiré n'est jamais débité |
| Clôture par version | Chaque attribution incrémente une version. Réservations, règlement et remboursements sont tous épinglés à la version qui a financé la requête : une modification par un administrateur en cours de requête ne peut donc pas rediriger le quota vers un autre lot |

Console : **Users → Set Welfare Balance**.

### 3. Codes de reprise

En amont, les codes de reprise ne font que recharger le portefeuille. Ce fork en fait un vrai mécanisme de distribution.

| Capacité | Détail |
| --- | --- |
| Trois types d'avantage | **Quota** (recharge du portefeuille), **Welfare** (solde d'aide sociale avec sa propre expiration), **Subscription** (attribue un plan) |
| Génération par lot | Créer 1 à 100 codes en une opération, partageant un même identifiant de lot |
| Règle de réclamation par lot | Par défaut chaque compte ne peut réclamer qu'un code par lot ; assouplissable par lot |
| Suppression par lot | Supprimer les codes sélectionnés, ou balayer tous les codes utilisés / expirés / désactivés |
| Export | Diffuser en flux les codes correspondants vers un fichier TXT, une clé par ligne |
| Expiration | Expiration propre au code, indépendante de celle de l'avantage |
| Audit | Chaque création, modification, suppression et export est journalisé |

Console : **Redemption Codes**. La reprise en libre-service reste dans **Wallet**.

### 4. Stratégie de facturation et repli sur les sources de financement

L'amont choisit la source de financement implicitement. Ce fork laisse chaque utilisateur choisir et rend le repli explicite.

| Stratégie | Comportement |
| --- | --- |
| Subscription First | Essaie l'abonnement, se replie sur le portefeuille si autorisé |
| Wallet First | Essaie le portefeuille, se replie sur l'abonnement si autorisé |
| Subscription Only | Ne touche jamais le portefeuille |
| Wallet Only | Ne touche jamais l'abonnement |

Le solde d'aide sociale est consommé automatiquement avant les deux, quand il peut couvrir la requête.

Console : **Wallet** — sélecteur de stratégie et entrée de reprise.

### 5. Groupes de jetons ordonnés

En amont, un jeton sélectionne un seul groupe. Ce fork le remplace par une liste de groupes ordonnée.

- Un jeton peut nommer plusieurs groupes, essayés dans l'ordre — la machinerie d'affinité de canal et de reprise existante décide du gagnant.
- Le choix des groupes est validé contre ce que le groupe de l'utilisateur peut réellement utiliser.
- La sélection ordonnée sert aussi de référence aux groupes d'utilisation d'abonnement et aux restrictions de plan.

Console : **Keys → Auto groups**.

### 6. Contrôles d'inscription et de journalisation

| Option | Effet |
| --- | --- |
| Restrict same-IP registration | Un seul compte par adresse IP. Appliqué par une table de déclarations unique plus un verrouillage par base de données (verrou consultatif sur PostgreSQL, lecture verrouillante sur MySQL, écriture unique sur SQLite) |
| Force IP logging | Outrepasse la préférence par utilisateur : chaque journal d'utilisation et d'erreur enregistre l'IP du client |

Console : **System Settings → Basic Authentication**.

### 7. Garanties comptables

Le chemin de facturation a été retravaillé pour survivre aux pannes, pas seulement au cas nominal.

| Mécanisme | Ce qu'il empêche |
| --- | --- |
| Enregistrements de remboursement durables | Une requête en échec écrit un enregistrement de remboursement avant de toucher aux soldes ; un worker réessaie jusqu'à aboutissement. Idempotent par ID de requête et par composant (financement, jeton) |
| Nouveaux essais bornés | Un remboursement définitivement impossible est parqué en `failed` après 20 tentatives, au lieu de réessayer indéfiniment toutes les 30 secondes |
| Rapprochement de règlement | Quand un ajustement de jeton échoue après validation du financement, la cible voulue est persistée et un worker la rattrape |
| Restauration de la préconsommation | Si la persistance des marqueurs de règlement de tâche échoue, la session revient à sa réservation durable, afin qu'un sondage ultérieur ne débite pas deux fois le même écart |
| Aide sociale cloisonnée par version | Réservations, règlement et remboursements portent tous la version d'attribution (voir plus haut) |

### 8. Durcissement de la sécurité

Correctifs appliqués par-dessus l'amont, chacun avec un test de régression :

| Correctif | Détail |
| --- | --- |
| Clôture des remboursements d'aide sociale | Une tâche d'aide sociale sans version d'attribution enregistrée est refusée, plutôt que remboursée dans le lot actuellement actif |
| Plans désactivés inaccessibles | Les codes de reprise ne peuvent pas attribuer un plan désactivé ; la création et la reprise vérifient toutes deux `enabled` |
| Fraîcheur du bail de concurrence | Un bail d'abonnement n'est réarmé que pour un enregistrement de préconsommation encore dans le TTL du bail : un ID de requête rejoué ne peut pas occuper une place qu'il ne détient plus |
| Validation des URL de notification | Les URL Webhook, Bark et Gotify sont validées contre la politique SSRF à l'enregistrement, pas seulement à la livraison |
| CVE des dépendances | 9 vulnérabilités dans `x/net`, `x/image` et `x/text` éliminées ; `govulncheck` ne signale plus rien |

### 9. Évolutions de la console

- **Users** — champ de solde d'aide sociale et action `Set Welfare Balance`
- **Keys** — éditeur d'ordre des groupes automatiques
- **Wallet** — cartes de plans d'abonnement, sélecteur de stratégie de facturation, entrée de reprise
- **Subscriptions** — liste des plans, achat et gestion en libre-service
- **Redemption Codes** — surface de gestion complète avec actions par lot et export
- **System Settings → Basic Authentication** — les deux contrôles IP ci-dessus

---

<a id="quick-start"></a>

## Démarrage rapide

```bash
docker run --name new-api -d --restart unless-stopped \
  -p 127.0.0.1:3000:3000 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  calciumion/new-api:latest
```

Ouvrez <http://localhost:3000> et terminez l'assistant de configuration.

Puis, dans l'ordre :

1. **Subscriptions** — créez un plan, ou ignorez cette étape si vous n'utilisez que le portefeuille
2. **Channels** — ajoutez une clé en amont et testez le canal
3. **Redemption Codes** — générez des codes pour le type d'avantage souhaité
4. Remettez un code à un utilisateur ; il l'utilise dans **Wallet**

La configuration, le stockage, le déploiement multi-nœuds et la référence complète des variables sont identiques à l'amont : voir [l'exemple d'environnement](./.env.example) et la [documentation amont](https://docs.newapi.ai/en/docs).

---

<a id="upstream"></a>

## Projet amont

Ce fork suit [QuantumNous/new-api](https://github.com/QuantumNous/new-api) et en fusionne les versions.

| | |
| --- | --- |
| Projet amont | <https://github.com/QuantumNous/new-api> |
| Basé sur | [One API](https://github.com/songquanpeng/one-api) (MIT) |
| Documentation | <https://docs.newapi.ai/en/docs> |
| Protocoles, canaux, plugins | Voir le README amont et [RelayKit](./relaykit/README.md) |

**Ce qui vient de l'amont sans modification** : routage des modèles et gestion des canaux, les quatre protocoles texte et leurs conversions, les points d'entrée image / audio / temps réel, le système de plugins de tâches JavaScript, Passkey / TOTP / OAuth / OIDC, les journaux d'audit, l'i18n, la coque de la console et le déploiement.

### Rester synchronisé avec l'amont

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream
git merge upstream/main
```

Les modifications du fork sont concentrées dans la facturation, les codes de reprise, les abonnements et l'aide sociale. Attendez-vous à des conflits dans `service/billing*.go`, `model/redemption.go`, `model/subscription.go`, `model/welfare_quota.go` et les fonctionnalités frontend correspondantes.

---

## Licence

Licence publique générale GNU Affero v3.0. Voir [LICENSE](./LICENSE), [NOTICE](./NOTICE) et [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md).

Ce projet est développé à partir de [One API](https://github.com/songquanpeng/one-api) (MIT) via [new-api](https://github.com/QuantumNous/new-api).

À utiliser légalement. Obtenez les clés en amont et l'accès aux modèles par des moyens autorisés, et respectez les conditions des fournisseurs ainsi que les lois de votre juridiction. Exploiter un service d'IA public emporte des obligations de déclaration, de sécurité des contenus et d'identification réelle dans de nombreuses juridictions.
