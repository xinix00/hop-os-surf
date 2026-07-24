# SURF-browser — de levende spec

Niet op websites jagen, maar gedrag vastleggen en dat **acen**. Dit document
is het plan in drie stappen: wat we hebben, hoe we aantonen dat het klopt,
en welke gaten we — op volgorde van hoe vaak het web ze gebruikt — dichten.
De websites zijn daarna de uitkomstmeting, niet het doel.

## 1. Wat er staat

| gebied | gedrag | fixture |
|---|---|---|
| document & netwerk | HTTPS (eigen CA-bundel), cookies, redirects, consent-gates, charset, webp/png/jpg/gif, echte fouten in de statusbalk | `session_test`, `consent_test` |
| cascade | cascadia-selectors, specificiteit + bronvolgorde, `@media` tegen de échte framebreedte, wij zijn light mode (`prefers-color-scheme: light`), `var()`, shorthands, `:is`/`:not`-vouwen, sr-only-patroonherkenning, alpha-0-kleuren zijn géén kleur | `media`, `verbergen`, `css_test` |
| tekst | drie letterschalen, vet, kleur, centreren, `white-space: pre`, links, lijsttekens — UA-stylesheet door dezelfde cascade | `tekst` |
| boxmodel | margin (incl. `auto`-centreren), padding, border, `width`/`max-width`, kleurvlakken, gedecoreerde inline-elementen | `boxmodel` |
| verbergen | `display:none`, `visibility`, `hidden`, `aria-hidden` op nav/aside, sr-only (1×1+clip), `opacity:0` | `verbergen` |
| flex | expliciete rij, `flex-wrap`, kolom(-reverse) reset de inline-context, `margin-left:auto`, `align-items:stretch` | `flex` |
| grid & tabel | `px`/`fr`/`%`/`auto`/`minmax`/`repeat`/`auto-fill`, tabel→grid, balanswissel kolommen↔stapelen | `grid` |
| positionering | sticky/fixed-top → gepinde header, fixed-bottom → cookiebar weg, absolute op de relative voorouder + late paint, aspect-ratio-fill | `positie` |
| afbeeldingen | `<img>`, floats, `background-image` (cover/tegels), logo-slot (alt-tekst-principe) | `plaatjes`, `absolute_test` |
| interactie | links + hit-test, scroll, tekstinvoer, GET-submit | `browse_test` |
| flex-uitlijning | `display:flex` is default een rij; `justify-content` (center/end/between/around/evenly); `flex-grow`/`flex-basis`/`width` als kolomgewichten | `flex-uitlijning` |
| grid-spans | `grid-column: 1 / -1` en `span N`: cellen over meerdere tracks, rij-plaatsing eromheen | `grid-span` |
| tekstopmaak | `text-transform` (upper/lower/capitalize); `text-decoration` door de cascade — links UA-default onderstreept, `none` van de site wint | `tekst-opmaak` |
| maten | `calc()` in lengtes (+ − × ÷); `min-height`/`height` rekken een vlak op (cap 600px) | `maten` |
| absolute ankers | `bottom`/`right`-anker (gelegd zodra de voorouder-onderkant bekend is) en shrink-to-fit: een badge is geen paginabrede balk | `absoluut-onder` |
| flex verticaal | de hoogste bepaalt de rij, de ouder groeit mee; `align-items`/`align-self` (center, flex-end; baseline ≈ end) — in kaartenrijen én inline rijen | `flex-verticaal` |
| flex volgorde | `order` (stabiel gesorteerd, DOM als tiebreaker), `flex-flow`-shorthand, `wrap-reverse` | `flex-volgorde` |
| flex ruimte | `justify-content` op kaartenrijen met vaste kolommen; `gap` met twee waarden / `row-gap` tussen gewrapte rijen; `place-items`/`place-content` | `flex-ruimte` |
| tekst rechts & lijsten | `text-align: right`; `list-style: none` (een menu is geen opsomming); `<ol>` telt echt (1. 2.) | `tekst-rechts` |
| moderne maten | `min()`/`max()`/`clamp()` in lengtes én font-size (onoplosbare vw → midden van min/max); `min-width`; gradient → eerste kleurstop | `maten-modern` |
| afbeeldingsmaten | `width`/`height`-attributen en CSS-maten op `<img>` (één maat schaalt proportioneel); lazy `data-src` en `srcset` laden mee | `plaatjes-maat` |
| tabel-spans | `colspan`: een cel over meerdere kolommen | `tabel-span` |
| dichtklappen | `<details>` zonder `open` toont alleen zijn `<summary>` | `dichtgeklapt` |
| beeldvulling | `object-fit: cover` snijdt bij i.p.v. pletten; `aspect-ratio` maakt de ontbrekende maat af | `plaatjes-vullen` |
| niet breken | `white-space: nowrap`: een label verhuist heel naar de volgende regel | `tekst-nowrap` |
| weggeklapt | `overflow: hidden` op `(max-)height` ~0 = de dichte accordeon-staat (padding-%-fotolijsten blijven) | `weggeklapt` |
| media-extra | `<video poster>`, `margin: 0 auto` op een blok-`<img>`, het zachte koppelteken (`&shy;`) | `media-extra` |
| beeldverhouding | CSS `height: auto` schakelt het height-attribuut uit (verhouding uit het beeld); hoogte-procenten zonder basis idem — geen bron-mengsels (wikipedia's ei-logo) | `beeldverhouding` |
| flex-wrap op maat | items met een eigen breedte (`calc(50% - 24px)` + marges — NRC) bepalen zelf hoeveel er per rij passen; de celwortel resolvet zijn width niet nóg eens | `flex-calc` |
| invoervelden | de site-CSS bepaalt de veldbreedte (wikipedia's 100%-zoekbalk); standaard blijft rand + wit vlak | `invoer` |
| randen | border-dikte (`3px` = geneste lijnen) en zijranden (`border-left: 6px solid` — meldingen, tabs) als gekleurde stroken; `transparent` is een ónzichtbare rand, geen grijze lijn | `randen` |
| svg | inline `<svg>`, `<img src="*.svg">` en svg-iconen gerasterd (tdewolff/canvas sinds 22-07 — echte gradients, strokes, transforms én `<text>`; puur Go, door de tamago-gate); maten per spec: CSS > attributen > default object size (300×150 op verhouding); `preserveAspectRatio` (default meet): passend in de doos, niet uitgerekt — ook als de host-doos een ándere verhouding heeft dan de symbol-viewBox (het NRC-patroon) | `svg`, `svg-maten`, `pasvorm` |
| off-canvas | `transform: translate(-100%…)` is een dichte lade; de −50%-centreertruc blijft staan | `verbergen` |
| absolute procenten | `right`/`left`-% tegen de containing-block-breedte, `top`/`bottom`-% tegen zijn gedeclareerde hoogte; ankers en `position` mogen in verschillende regels staan; de origin volgt `width` + `margin: 0 auto`-centrering; `height` reserveert ruimte, ook op kale containers en spacers — wikipedia's talencirkel rendert | `wikipedia-cirkel` |
| rem-basis | `html { font-size: 62.5% }` maakt 1rem = 10px: alle rem/em-maten rekenen tegen de wortel-lettergrootte | `wikipedia-kop` |
| kop-patroon | de top-marge van het allereerste blok telt (met echte marge-collapse); sprite-sheets: `background-position` knipt het juiste plaatje uit (wordmarks, zuster-iconen); `text-align: center` lijnt uit binnen de eigen (margin-auto-)container, niet dubbel tegen de paginarand | `wikipedia-kop` |
| inline-block | `display: inline-block` met een breedte is een mini-blok in de regelflow — tegels naast elkaar (het float:left-gevoel) | `inline-blok` |
| grid volwaardig | `calc()` met haakjes en `*`/`/`-voorrang in sporen; regelnamen (`[content-start]`); `fit-content(X)`; smalle vaste sporen (0.75rem-rails); tot 6 kolommen; het centreer-spoor `1fr <vast> 1fr` = centrering; `grid-auto-flow: column` zonder template = één rij | `grid-vol` |
| vastgezet | `position: fixed` volwaardig: de viewport is de containing block — `right: 0`-panelen, `height: calc(100% - var(--balk))` tegen de vensterhoogte (viewH, live uit het window), een top+bottom-paar zónder height is óók een hoogte; los van de flow, bovenop geschilderd (tweakers' tracker-rail) | `vastgezet` |
| svg-symbolen | `<svg><use href="#id">` én `href="sheet.svg#id"`: het symbool wordt vóór de layout ingelijmd (externe sprite-sheets opgehaald, tweakers' 217-symbolen-vel), de omhullende svg erft de viewBox; defs/symbolen renderen zelf nooit | `svg-symbolen` |
| drijvend | `float: left/right` op élk element: shrink-to-fit of CSS-width, de flow stroomt ernaast, `clear` en de impliciete clear bij bloksluiting; de **float-rij** — opeenvolgende floats rijgen aan elkaar en wrappen pas als het niet meer past (NRC's header: float:left-knoppen in een float:right-balk) | `drijvend` |
| maat-attributen | het `width`/`height`-attribuut van svg's en tabellen is een presentational hint — gewone CSS op de laagste cascade-plek; `<td width>` pint zijn tabelkolom, de rest verdeelt wat overblijft (`<img>` houdt zijn eigen verhoudingsregel) | `maat-attributen` |
| marge-zijden | de `margin`/`padding`-shorthand expandeert bij het parsen naar zijn vier longhands: een látere `margin: 0` reset dus echt een eerdere `margin-left` (cascade-volgorde), in élke mengvorm | `marge-zijden` |
| tweakers-kop | de échte tweakers-menubalk, integraal: `:is()` mét combinators herschreven naar cascadia-taal (`.more:is(:is(twk-site-menu>menu)>li)>.dropdown-menu` — de dropdowns zijn nu echt dicht); de fixed balk ontsnapt aan de gecentreerde pagina-rail (viewport = containing block, volle breedte); verborgen kinderen zijn geen flex-items (hamburger, sr-kop); zonder `flex-wrap` is nowrap de default — een menubalk met veel linkjes blijft één regel; `margin-inline`/`padding-inline`/`-block`; `margin-inline: auto` in een rij centreert (half duwen) | `tweakers-kop` |
| stipjes | een leeg element mét eigen maat en achtergrond ís het vlak — carrousel-stipjes, statuslampjes; losse vlakjes smelten niet samen tot een balk | `stipjes` |
| afgerond | `border-radius`: vlakken en randen met ronde hoeken (px, `50%` = helemaal rond, pil-waarden klemmen op de halve maat); op een `<img>` worden de hoeken doorzichtig — de ronde avatar | `afgerond` |
| sprite-vel | wikipedia's geneste sprite-sheet: sub-`<svg id>`'s apart gerasterd en op hun x/y samengelegd (geneste svg's met een y-offset kan ook canvas niet — de composiet blijft nodig); `background-position: 0 0` op een gróter vel is een crop; de gradient-vóór-de-url-fallback laat de url winnen — het wordmark en de zusterlogo's renderen | `sprite-vel` |
| logo-naam | zonder JS blijft een `<twk-icon>` leeg: een leeg element met `role="img"` + `aria-label` rendert zijn label (het alt-principe); het logo-slot blijft alléén het site-icoon — geen verzonnen naam ernaast, het echte wordmark bevat de merknaam zelf | `logo-naam` |
| reuzefoto's | spaties in URL's worden ge-encodeerd zoals elke browser doet ("waarom 1.webp"); de decode-grens is een píekbudget per formaat (jpeg/webp ~2 B/px, png 4) i.p.v. een zijde-cap — 24-megapixel-webp's laden en worden meteen teruggeschaald naar ≤2048/zijde (easyflorist) | `TestReuzefotoEnSpatie` |
| centreren in vakken | `text-align` erft gewoon dóór een absoluut gepositioneerd vak in — wikipedia's `.central-featured` centreert zo de tekst in zijn talenvakken | `wikipedia-cirkel` |
| flex op de bodem | een flex-item zónder maat en zonder grow is content-sized (grow ís per spec 0): de kolommen komen uit een méétpas, en justify-content heeft echte vrije ruimte te verdelen — easyflorists space-between-header zet de knoppen uiterst rechts; bij space-between/end raakt het laatste item de containerrand; grow-mix: vast + `flex: 1` + content werkt door elkaar | `flex-bodem` |
| grid op de bodem | `grid-template-areas`: benoemde gebieden — rijen letterlijk uit de template, kolomspans uit naam-herhaling (holy grail: kop/zij/hoofd/voet); `justify-items`/`justify-self`: het item krimpt naar zijn inhoud en staat midden of tegen de celrand; rowspans en gaten vallen eerlijk terug op stapelen | `grid-bodem` |
| gestylede invoer | de site-CSS kleedt velden en knoppen aan: achtergrond, tekstkleur, rand, `border-radius` (de pil-knop!), `height`/padding; het `placeholder`-attribuut toont grijs tot er getikt wordt; zonder CSS blijft de UA-default | `invoer-stijl` |
| @import | sheets die sheets laden bestaan echt: de import wordt op zijn plek ingelijmd (relatief opgelost tegen de importerende sheet, recursief binnen het budget), een mediaconditie wordt een omhullend `@media`-blok, `supports(...)` evalueert mee | `sheet-import` |
| @supports | de conditie evalueert tegen `supportedProp` — dezelfde waarheid als de regel-filter, geen tweede lijst; `and`/`or`/`not` en geneste haakjes; onbekende vormen (`selector(...)`) zijn niet waar, net als in een browser die ze niet kent | `supports` |
| inherit + currentColor | `inherit` is letterlijk "wat de stijl-struct al meedraagt" (de declaratie vervalt — `color: inherit` op links, `text-align: inherit`); `currentColor` wordt de cascade-kleur zodra die bekend is: randen, vlakken én `fill` op inline svg's | `erf-kleur` |
| box-sizing | onze `width` rekent van nature als border-box — het vlak ís de gedeclareerde maat, padding zit erbinnen; precies wat `*{box-sizing:border-box}` van het moderne web vraagt (gratis vinkje, nu bewaakt) | `doosmaat` |
| de regel-kap | `-webkit-line-clamp: N` kapt na N regels met `...`; `white-space:nowrap` + `text-overflow:ellipsis` is de éénregel-variant — teaser-kaarten lopen niet meer vol | `regel-klem` |
| vensterunits | `vw`/`vh` (en `dvh`/`svh`/`lvh`) als gewone lengtes: tegen de layoutbreedte en de vensterhoogte — hero's van `50vh` zijn echt half venster | `venster-maten` |
| line-height | de leesbaarheidsknop: een kale factor of procent × de regelhoogte, een lengte tegen de gedeclareerde font-size — geklemd op [1, 16] px interlinie; de ruimste tekst op de regel wint | `regelhoogte` |
| het HTML-strootje | de UA-sheet dekt `blockquote` (inspringing + balkje), `s`/`del` (line-through — de oude prijs!), `ins`/`u`, `sub`/`sup` (klein + offset onder/boven de regel), `dl`/`dd`, `kbd` (toets-chip); alleen het middenstreepje en de offset waren engine-werk | `ua-elementen` |
| tabel-rowspan | een `rowspan` bezet zijn kolom ook in de rijen eronder: de cel staat één keer, de kolommen eronder blijven leeg maar de uitlijning klopt | `rijspan` |
| celmidden + lagen | `vertical-align: top/middle/bottom` is de tabel-taal voor align-items — per cel vertaald; `z-index` sorteert de late (absolute) schilderlaag stabiel, dus gelijke z blijft bronvolgorde | `celmidden-lagen` |
| progressiebalkjes | het gethop-patroon: een leeg blok mét gedeclareerde hoogte houdt zijn vlak (het spoor), en een leeg absoluut element met vlak, width-% en inset-ankers rendert als vlak op maat van zijn relative ouder (de vulling) — fillAbs laat lege elementen door (een leeg element is geen fotolijst-vulling), en de containing block ligt exact op het vlak | `voortgang` |
| scrollen zonder hertekenen | de layout wás al gecached; RenderScrolled hergebruikt ook de píxels: het overlevende deel van het frame schuift in het buffer zelf (memmove), alleen de blootgelegde strook wordt getekend (geclipt via SubImage) en de damage naar de compositor is het contentgebied — geen byte extra opslag; pin-overgangen en buffer/pagina-wissels vallen terug op een volle Render | `TestRenderScrolled` (bit-identiek aan een volle render, álle fixtures × deltas heen en terug) |
| geen synthetische marges | subs (cellen, blok-in-blok) rekenen zónder onze paginamarge: de celbreedte ís de contentbreedte — voorheen verdween er 12px per nestingniveau (tweakers' hero: 704 → 599); de paginamarge (6px, het body-margin-gevoel) blijft alleen op de wortel | `doosmaat`, `beeld-vol` |
| fixed = (0,0) | een gepinde fixed top:0-balk begint óp de hoek: rand tot rand (geen paginamarge), geen blokmarge, en hij ontsnapt aan zijn wrapper zolang er geen echte inhoud boven staat | `tweakers-kop` (`volbreed`, `bovenrand`) |
| beeld op 100% | `width: 100%` betekent ook uitrekken bóven de natuurlijke maat, en de teaser-float (thumbnail links, tekst ernaast) geldt alleen als het beeld hoogstens de halve regel vraagt — een vol beeld ís de inhoud | `beeld-vol` |
| grid-areas volledig | de areas-template is heilig: een gat ("." of slot zonder element) is een lege cel, een rowspan-naam houdt zijn kolom bezet (inhoud staat één keer), auto-geplaatste kinderen komen in impliciete rijen over dezelfde tracks, en de balans-heuristiek blijft van expliciete templates af — tweakers' "editorial-content editorial-content sidebar" rendert mét zijkolom | `grid-areas-vol` |

## 2. Zo tonen we aan dat het klopt

Elke fixture in `app/browse/testdata/spec/` is echt HTML+CSS met onderin een
verwachtingenblok — leesbaar, en het draait in de poort:

```html
<script type="text/x-expect">
rechtsvan "menuknop" "merknaam"
evenhoog "kort stukje" "deze kaart"
</script>
```

- `go test ./app/browse -run Spec` draait alles offline (handler-transport)
  en schrijft het **contactvel** `docs/browser-spec.png` — elke fixture zoals
  de browser hem rendert, elke run vers.
- `*.todo.html` zijn de open gaten: ze staan wél op het vel, maar hun
  verwachtingen tellen pas mee met `SPEC_TODO=1` (rood = de klus). Gat
  gedicht → `.todo` uit de naam → de poort bewaakt het voortaan.
- `tools/test.sh` blijft de eindpoort: host-tests + tamago-builds.

De woordenlijst (alles meet op de gelayoute pagina): `zichtbaar`/`verborgen`,
`onder`, `rechtsvan`, `overlapt`, `bovenop` (schildervolgorde), `kleur`,
`achtergrond`, `rand`, `vet`/`nietvet`, `schaal`, `link`, `gecentreerd`,
`rechterrand`, `ingesprongen`, `vlakbreedte`/`vlakhoogte`/`vlakmidden`,
`bredervlak`, `evenhoog`, `tegel`, `plaatjes`, `gepind`, `donker`,
`onderstreept`, `vlakjes N W H #kleur` (losse maatvlakjes zonder tekst —
stipjes), `rond` (het vlak heeft een hoekstraal), `rondeplaat W H` (de
afbeelding heeft doorzichtige hoeken), `geenrand`, `pasplaat W H`
(passend gemaakt, niet uitgerekt: hoek doorzichtig, midden dekkend),
`hmidden` (tekst horizontaal in het midden van zijn vlak),
`veldkleur`/`veldrond`/`veldhoog`/`veldkaal` (de stijl van een veld, op
zijn name-attribuut), `randkleur` (de rand van het vlak in precies deze
kleur), `doorgestreept`, `lager`/`hoger` (sub/sup-offset op de regel),
`zelfdekolom` (kolomuitlijning — rowspan), `regelafstand a b min max`
(de line-height-pitch tussen twee regelstarts), en de richtlijn
`breedte N` (schakelt de framebreedte).

## 3. De gaten, op volgorde van web-gebruik

De eerste vijf zijn op 21-07 gedicht (fixtures hernoemd, de poort bewaakt
ze — zie de laatste vijf rijen van §1). Wat er nog open staat:

| # | gat | wat er mist | waar je het ziet |
|---|---|---|---|
| 1 | data:-URI's | `background-image: url(data:image/svg+xml...)` en `<img src="data:...">` decoderen | logo's en iconen overal |
| 2 | grid-rijspans | areas mét rowspan is gedicht (23-07, `grid-areas-vol`); alleen de `grid-row: span N`-property zelf ontbreekt nog | voorpagina's |
| 3 | interactietrack | POST-formulieren, checkbox/radio, `<select>` | zoeken, inloggen |
| 4 | glyphs | ASCII-dekking van het font (→ transliteratie-tabel) | namen, koersen |

Gedicht op 22-07: svg `<use>`-symbolen (intern én externe sheets — NRC's
wordmark en tweakers' categorie-iconen renderen live), position:fixed
volwaardig, floats + de float-rij, maat-attributen als presentational
hints, en de margin/padding-longhand-cascade. Kanttekening: tweakers'
eigen site-logo (`<twk-icon>`) blijft leeg over de lijn — hun JS hangt de
`<svg><use>` er pas client-side in; het logo-slot (site-icoon) blijft daar
de eerlijke weergave. Elke server-gerenderde `<use>` doet het wél.

Ook gedicht op 22-07 (de sloterlijst): de stille regel-verliezers
(`@import`, `@supports`, `inherit`/`currentColor`), de zichtbare laag
(line-clamp/ellipsis, `vw`/`vh`, `line-height`, box-sizing-check) en de
UA-stylesheet-ronde met line-through, sub/sup, tabel-rowspan,
`vertical-align` en `z-index` — zie de laatste elf rijen van §1.

Bewust overgeslagen: transitions/animations/transform (op de
offcanvas-translate-detectie na), box-shadow, letter-spacing, cursor —
dat is het web dat beweegt, en wij zijn het web dat leest.

## 4. Werkwijze per gat

1. eerst de fixture, als `<naam>.todo.html` — `SPEC_TODO=1 go test
   ./app/browse -run Spec` laat zien wat er rood is;
2. implementeren tot de fixture groen is — het échte mechanisme, geen
   raad-heuristieken;
3. `.todo` uit de bestandsnaam: vanaf dan bewaakt de poort het gedrag;
4. `tools/test.sh` groen — en dán pas de site-schoten
   (`go test ./app/browse -run ScreenshotSites`) verversen en kijken wat het
   op tweakers/nrc/nu/gethop oplevert.
