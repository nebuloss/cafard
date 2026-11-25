package i18n

// fr is the French for each English format, by the file the English is in.
// The words are the design doc's glossary, as the web UIs say them:
// personne, agent, appareil, membre, projet, conversation, message,
// fichier, connexion, lien, jeton. A space before : ; ? ! is a no-break
// space (\u00a0), and the test refuses a plain one there.
var fr = map[string]string{
	// service/access/access.go
	"%q is not a name cafard accepts: use 2 to 32 lowercase letters, digits and hyphens, like %s.":                "%q n'est pas un nom accepté par cafard\u00a0: utilisez de 2 à 32 minuscules, chiffres et tirets, comme %s.",
	"%q is not a project name cafard accepts: use 2 to 32 lowercase letters, digits and hyphens, like speedtest.": "%q n'est pas un nom de projet accepté par cafard\u00a0: utilisez de 2 à 32 minuscules, chiffres et tirets, comme speedtest.",
	"%q is a word cafard's addresses use: choose another project name.":                                           "%q est un mot réservé aux adresses de cafard\u00a0: choisissez un autre nom de projet.",
	"%s has been removed from cafard.":                                                          "%s a été retiré de cafard.",
	"%s is paused. An admin can switch it back on.":                                             "%s est en pause. Un admin peut le réactiver.",
	"%s is not a member of %s. Ask a person in the project to add it.":                          "%s n'est pas membre de %s. Demandez à une personne du projet de l'ajouter.",
	"%s is an agent: only people manage access.":                                                "%s est un agent\u00a0: seules les personnes gèrent les accès.",
	"%s is a device: only people manage access.":                                                "%s est un appareil\u00a0: seules les personnes gèrent les accès.",
	"%s is not an admin.":                                                                       "%s n'est pas admin.",
	"%s is a device: it sends to %s and reads nothing.":                                         "%s est un appareil\u00a0: il envoie à %s et ne lit rien.",
	"%s is archived (read-only). You can read it; nothing can be posted.":                       "%s est archivé (lecture seule). Vous pouvez le lire\u00a0; rien ne peut y être publié.",
	"%s is a device: it sends to projects, not to members.":                                     "%s est un appareil\u00a0: il envoie à des projets, pas à des membres.",
	"%s is a device: it reads nothing, so it cannot be written to.":                             "%s est un appareil\u00a0: il ne lit rien, on ne peut donc pas lui écrire.",
	"A direct message needs someone else.":                                                      "Un message direct s'adresse à quelqu'un d'autre.",
	"%s and %s share no project. A direct message needs a project in common.":                   "%s et %s n'ont aucun projet en commun. Un message direct en demande un.",
	"%s is already a member of %s.":                                                             "%s est déjà membre de %s.",
	"%s is not a member of %s.":                                                                 "%s n'est pas membre de %s.",
	"%s is an agent: only people add or remove members.":                                        "%s est un agent\u00a0: seules les personnes ajoutent ou retirent des membres.",
	"%s is a device: only people add or remove members.":                                        "%s est un appareil\u00a0: seules les personnes ajoutent ou retirent des membres.",
	"%s is archived: its members cannot change until it is unarchived.":                         "%s est archivé\u00a0: ses membres ne peuvent pas changer tant qu'il n'est pas désarchivé.",
	"%q is not a kind: use person, agent or device.":                                            "%q n'est pas un type\u00a0: utilisez person, agent ou device.",
	"%s already exists. Names are shared by people, agents and devices, removed ones included.": "%s existe déjà. Les noms sont communs aux personnes, aux agents et aux appareils, retirés compris.",
	"You cannot do that to yourself.":                                                           "Vous ne pouvez pas faire cela sur vous-même.",
	"Retention and quota must both be more than zero.":                                          "La conservation et le quota doivent tous deux être supérieurs à zéro.",
	"%s already exists.":                                                                        "%s existe déjà.",
	"%s does not exist, or has been removed.":                                                   "%s n'existe pas, ou a été supprimé.",

	// service/access/admin.go
	"Use at least %d characters.": "Utilisez au moins %d caractères.",
	"Use at most %d characters.":  "Utilisez au plus %d caractères.",

	// service/access/links.go
	"A link waits 7 days at most.": "Un lien attend 7 jours au plus.",
	"This link no longer works: it was used, cancelled, or waited too long. Ask an admin for a new one.": "Ce lien ne fonctionne plus\u00a0: il a servi, a été annulé, ou a trop attendu. Demandez-en un nouveau à un admin.",
	"This link sets up a machine: run the command it shows on that machine.":                             "Ce lien configure une machine\u00a0: exécutez la commande qu'il affiche sur cette machine.",
	"This link is a person's: open it in a browser.":                                                     "Ce lien est celui d'une personne\u00a0: ouvrez-le dans un navigateur.",
	"This link was used a moment ago. Ask an admin for a new one.":                                       "Ce lien vient de servir. Demandez-en un nouveau à un admin.",
	"That link no longer works already.":                                                                 "Ce lien ne fonctionne déjà plus.",

	// service/access/phone.go
	"Only people sign in; %s has a token.":                                      "Seules les personnes se connectent\u00a0; %s a un jeton.",
	"This is not a sign-in code.":                                               "Ce n'est pas un code de connexion.",
	"This link was used a moment ago. Ask for a new one.":                       "Ce lien vient de servir. Demandez-en un nouveau.",
	"Send the new text as {\"text\": \"…\"}.":                                   "Envoyez le nouveau texte sous la forme {\"text\": \"…\"}.",
	"No such message.":                                                          "Aucun message de ce nom.",
	"Only the member who sent a message can change or delete it.":               "Seul le membre qui a envoyé un message peut le modifier ou le supprimer.",
	"A message with no file needs text: delete it instead.":                     "Un message sans fichier a besoin de texte\u00a0: supprimez-le plutôt.",
	"That message is deleted.":                                                  "Ce message est supprimé.",
	"No message %d.":                                                            "Aucun message %d.",
	"That message is already deleted.":                                          "Ce message est déjà supprimé.",
	"A file of that message is still arriving. Delete it once the upload ends.": "Un fichier de ce message est encore en cours d'envoi. Supprimez-le une fois l'envoi terminé.",
	"Only the member who sent a file can delete it.":                            "Seul le membre qui a envoyé un fichier peut le supprimer.",

	// service/access/signin.go
	"You are signed out. Sign in again.": "Vous n'êtes plus connecté. Connectez-vous à nouveau.",
	"That sign-in has already ended.":    "Cette connexion est déjà terminée.",

	// service/access/tokens.go
	"%s is a person: people sign in, and have no token.":                                            "%s est une personne\u00a0: les personnes se connectent, et n'ont pas de jeton.",
	"That is not a cafard token: it starts with cfd_ag_ or cfd_dv_.":                                "Ce n'est pas un jeton cafard\u00a0: il commence par cfd_ag_ ou cfd_dv_.",
	"This token is not valid: it was never issued, or it was replaced. Ask an admin for a new one.": "Ce jeton n'est pas valide\u00a0: il n'a jamais été émis, ou il a été remplacé. Demandez-en un nouveau à un admin.",
	"This token is not valid.":                       "Ce jeton n'est pas valide.",
	"Member %d does not exist, or has been removed.": "Le membre %d n'existe pas, ou a été supprimé.",

	// service/chat/chat.go
	"%q is not a conversation name cafard accepts: use lowercase letters, digits and hyphens, like wifi-drops.": "%q n'est pas un nom de conversation accepté par cafard\u00a0: utilisez des minuscules, des chiffres et des tirets, comme wifi-drops.",
	"The message is empty.": "Le message est vide.",
	"The message is %d bytes; the most is %d. Send long text as a file, where it can be read by page.": "Le message fait %d octets\u00a0; le maximum est %d. Envoyez un long texte en fichier, qui se lit page par page.",
	"The message is not UTF-8 text. Send it as a file.":                                                "Le message n'est pas du texte UTF-8. Envoyez-le en fichier.",
	"The caption is too long.":  "La légende est trop longue.",
	"No project %q.":            "Aucun projet %q.",
	"No conversation %q in %s.": "Aucune conversation %q dans %s.",
	"No file %d.":               "Aucun fichier %d.",
	"%s was cut off: the server stopped while it was arriving.": "%s a été coupé\u00a0: le serveur s'est arrêté pendant son arrivée.",
	"%s was deleted.": "%s a été supprimé.",

	// service/chat/lines.go
	"%s is %s, not text: read it whole, or open it as an image.": "%s est de type %s, pas du texte\u00a0: lisez-le en entier, ou ouvrez-le comme image.",

	// service/chat/manage.go
	"Write a first message, or give the conversation a title.":                                                                  "Écrivez un premier message, ou donnez un titre à la conversation.",
	"The message is too long. Send long text as a file.":                                                                        "Le message est trop long. Envoyez un long texte en fichier.",
	"A file is added only to your own message, sent in the last few minutes, in this conversation. Send it on its own instead.": "Un fichier ne s'ajoute qu'à votre propre message, envoyé dans les dernières minutes, dans cette conversation. Envoyez-le plutôt seul.",
	"A title is 1 to 120 characters.":                                                                                           "Un titre fait de 1 à 120 caractères.",
	"No member %q.":                                                                                                             "Aucun membre %q.",
	"Only people add members.":                                                                                                  "Seules les personnes ajoutent des membres.",
	"%s is still arriving. Delete it once the upload ends.":                                                                     "%s est encore en train d'arriver. Supprimez-le une fois l'envoi terminé.",
	"%s is already gone.":                                                                                                       "%s n'existe déjà plus.",
	"Name the file at the end of the address: …/send/<project>/<conversation>/boot.log.":                                        "Nommez le fichier à la fin de l'adresse\u00a0: …/send/<project>/<conversation>/boot.log.",

	// httpapi/api/api.go and chat.go
	"No route %s %s. The routes are listed in `go doc cafard/internal/httpapi/api`.":                                           "Aucune route %s %s. Les routes sont listées dans `go doc cafard/internal/httpapi/api`.",
	`Sign in, or send a token: -H "Authorization: Bearer cfd_…". An admin makes one with cafard admin create agent or device.`: "Connectez-vous, ou envoyez un jeton\u00a0: -H \"Authorization: Bearer cfd_…\". Un admin en crée un avec cafard admin create agent ou device.",
	"Writes from another site are refused.":                                                "Les écritures venant d'un autre site sont refusées.",
	"cafard could not do that; the error is in its log.":                                   "cafard n'a pas pu le faire\u00a0; l'erreur est dans son journal.",
	"The request is not the JSON this route takes: %s":                                     "La requête n'est pas le JSON qu'attend cette route\u00a0: %s",
	"The upload reached the %d MB a request may send; what arrived before that is stored.": "L'envoi a atteint les %d Mo qu'une requête peut envoyer\u00a0; ce qui est arrivé avant est enregistré.",
	"The upload ended early; what arrived is stored.":                                      "L'envoi s'est arrêté avant la fin\u00a0; ce qui est arrivé est enregistré.",
	"No such file.":    "Ce fichier n'existe pas.",
	"No such sign-in.": "Cette connexion n'existe pas.",
	"Only a person signed in to the chat app can sign in another phone.": "Seule une personne connectée au chat peut connecter un autre téléphone.",
	"Too many attempts. Wait a minute.":                                  "Trop de tentatives. Attendez une minute.",
	"This server cannot wait.":                                           "Ce serveur ne peut pas attendre.",
	"No project %q among yours.":                                         "Aucun projet %q parmi les vôtres.",
	"This server cannot stream events.":                                  "Ce serveur ne peut pas diffuser d'événements.",

	// httpapi/admin/admin.go
	"No route %s %s.": "Aucune route %s %s.",
	"Too many attempts. Try again in %d seconds.":                                                  "Trop de tentatives. Réessayez dans %d secondes.",
	"No admin password is set. Set one on the box with cafard passwd, then restart cafard.":        "Aucun mot de passe admin n'est défini. Définissez-en un sur la machine avec cafard passwd, puis redémarrez cafard.",
	"That is not the admin password.":                                                              "Ce n'est pas le mot de passe admin.",
	"An agent's token is never shown: make a new install link, and run it on the agent's machine.": "Le jeton d'un agent n'est jamais affiché\u00a0: créez un nouveau lien d'installation, et exécutez-le sur la machine de l'agent.",
	"Type the project's name to confirm: deleting it cannot be undone.":                            "Saisissez le nom du projet pour confirmer\u00a0: sa suppression est définitive.",
	"No such link.": "Ce lien n'existe pas.",
	// The argument is config.CheckPublicURL's reason, which stays English.
	"The public URL %s.": "URL publique refusée\u00a0: %s.",

	// httpapi/admin/notices.go
	"The join link for %s expired unused":                     "Le lien d'invitation de %s a expiré sans servir",
	"The install link for %s expired unused":                  "Le lien d'installation de %s a expiré sans servir",
	"Make a new one from its page.":                           "Créez-en un nouveau depuis sa page.",
	"%s has not connected for %d days":                        "%s ne s'est pas connecté depuis %d jours",
	"Its token still works. Pause it if the machine is gone.": "Son jeton fonctionne toujours. Mettez-le en pause si la machine n'existe plus.",
	"%s is in no project":                                     "%s n'est dans aucun projet",
	"It can reach nothing until it is put in one.":            "Il n'a accès à rien tant qu'on ne l'ajoute pas à un projet.",
	"%s is at its quota: uploads are refused":                 "%s a atteint son quota\u00a0: les envois sont refusés",
	"Raise the quota, or shorten the retention.":              "Augmentez le quota, ou raccourcissez la conservation.",
	"%s is at %.0f%% of its quota":                            "%s est à %.0f\u00a0%% de son quota",
	"Uploads stop at 100%%.":                                  "Les envois s'arrêtent à 100\u00a0%%.",
	"%s has no members":                                       "%s n'a aucun membre",
	"Name between 1 and 500 notices.":                         "Indiquez entre 1 et 500 notifications.",

	// httpapi/join/join.go and page.go
	"Too many attempts from this address. Wait a minute.": "Trop de tentatives depuis cette adresse. Attendez une minute.",
	"Cross-site requests are refused.":                    "Les requêtes venant d'un autre site sont refusées.",
	"Set up %s":                                           "Installer %s",
	"Sign in":                                             "Se connecter",
	"Sign in as %s":                                       "Se connecter en tant que %s",
	"This link no longer works":                           "Ce lien ne fonctionne plus",
	"Run on its machine, as the user its agents run as.": "À lancer sur sa machine, sous l'utilisateur qui fait tourner ses agents.",
	"On this device, as":    "Sur cet appareil, en tant que",
	"Works once, until %s.": "Valable une fois, jusqu'au %s.",
}
