// Оригинальный знак продукта (отзыв 13.11): «пара бадди» — большой синий пузырь-реплика (buddy) и маленький зелёный
// (стажёр). На больших размерах видны лица и аксессуар; на маленьких (шапка, аватар в чате) остаются два пузыря с
// глазами-точками, которые читаются и на 28 px. Персонажа переключает BUDDY_VARIANT (эскизы:
// docs/design/buddy-icon-variants.png).

type Face = 'open' | 'wink' | 'glasses' | 'happy'
type SmallFace = 'smile' | 'wow' | 'wink'
type Accessory = 'sprout' | 'question' | 'antenna' | 'headset' | 'star'

interface Variant {
  big: Face
  small: SmallFace
  accessories: Accessory[]
}

/** E1 подмигивание · E2 росток · E3 вопрос — ответ · E4 на связи · E5 очки · E6 радость · E7 гарнитура · E8 росток + антенна */
export const BUDDY_VARIANTS: Record<string, Variant> = {
  E1: { big: 'wink', small: 'smile', accessories: [] },
  E2: { big: 'wink', small: 'smile', accessories: ['sprout'] },
  E3: { big: 'wink', small: 'wow', accessories: ['question'] },
  E4: { big: 'open', small: 'wink', accessories: ['antenna'] },
  E5: { big: 'glasses', small: 'smile', accessories: ['sprout'] },
  E6: { big: 'happy', small: 'smile', accessories: ['star'] },
  E7: { big: 'wink', small: 'wow', accessories: ['headset', 'question'] },
  E8: { big: 'wink', small: 'smile', accessories: ['sprout', 'antenna'] },
}
export const BUDDY_VARIANT = 'E2'

const BIG = 'M22 34h62a12 12 0 0 1 12 12v30a12 12 0 0 1-12 12H50l-14 13V88H22a12 12 0 0 1-12-12V46a12 12 0 0 1 12-12z'
const SMALL = 'M76 72h44a10 10 0 0 1 10 10v20a10 10 0 0 1-10 10h-6v10l-11-10H76a10 10 0 0 1-10-10V82a10 10 0 0 1 10-10z'
const BLUE = '#00aaff'
const GREEN = '#97cf26'
const INK = '#1a1a1a'

function BigFace({ face }: { face: Face }) {
  const eye = (cx: number) => (
    <>
      <circle cx={cx} cy="58" r="8" fill="#fff" />
      <circle cx={cx + 1} cy="60" r="4" fill={INK} />
    </>
  )
  return (
    <>
      {face === 'open' && (
        <>
          {eye(42)}
          {eye(66)}
        </>
      )}
      {face === 'wink' && (
        <>
          {eye(42)}
          <path d="M59 60q7-7 14 0" stroke="#fff" strokeWidth="4" fill="none" strokeLinecap="round" />
        </>
      )}
      {face === 'glasses' && (
        <>
          <circle cx="42" cy="58" r="9" fill="#fff" stroke={INK} strokeWidth="3" />
          <circle cx="66" cy="58" r="9" fill="#fff" stroke={INK} strokeWidth="3" />
          <path d="M51 58h6" stroke={INK} strokeWidth="3" />
          <circle cx="43" cy="60" r="3.5" fill={INK} />
          <circle cx="67" cy="60" r="3.5" fill={INK} />
        </>
      )}
      {face === 'happy' && <path d="M35 60q7-8 14 0M59 60q7-8 14 0" stroke="#fff" strokeWidth="4" fill="none" strokeLinecap="round" />}
      <path d="M45 72q9 7 18 0" stroke="#fff" strokeWidth="4" fill="none" strokeLinecap="round" />
      <circle cx="31" cy="70" r="4" fill="#ff6163" opacity=".5" />
      <circle cx="77" cy="70" r="4" fill="#ff6163" opacity=".5" />
    </>
  )
}

function SmallFace({ face }: { face: SmallFace }) {
  const smile = <path d="M93 100q6 5 12 0" stroke="#fff" strokeWidth="3" fill="none" strokeLinecap="round" />
  if (face === 'wow')
    return (
      <>
        <circle cx="90" cy="89" r="5.5" fill="#fff" />
        <circle cx="108" cy="89" r="5.5" fill="#fff" />
        <circle cx="90" cy="90" r="2.5" fill={INK} />
        <circle cx="108" cy="90" r="2.5" fill={INK} />
        <ellipse cx="99" cy="102" rx="3.5" ry="4" fill={INK} />
      </>
    )
  return (
    <>
      <circle cx="90" cy="90" r="5" fill="#fff" />
      <circle cx="91" cy="91" r="2.5" fill={INK} />
      {face === 'wink' ? (
        <path d="M103 91q5-5 10 0" stroke="#fff" strokeWidth="3" fill="none" strokeLinecap="round" />
      ) : (
        <>
          <circle cx="108" cy="90" r="5" fill="#fff" />
          <circle cx="109" cy="91" r="2.5" fill={INK} />
        </>
      )}
      {smile}
    </>
  )
}

function Accessories({ items }: { items: Accessory[] }) {
  return (
    <>
      {items.includes('headset') && (
        <>
          <path d="M14 54a40 34 0 0 1 80 0" stroke={INK} strokeWidth="4" fill="none" />
          <rect x="6" y="50" width="11" height="20" rx="5" fill="#965eeb" />
          <rect x="91" y="50" width="11" height="20" rx="5" fill="#965eeb" />
        </>
      )}
      {items.includes('antenna') && (
        <>
          <line x1="53" y1="34" x2="53" y2="22" stroke="#0a7fd4" strokeWidth="4" strokeLinecap="round" />
          <circle cx="53" cy="20" r="6" fill={GREEN} />
        </>
      )}
      {items.includes('sprout') && (
        <>
          <path d="M98 72v-8" stroke="#4a8f1a" strokeWidth="3" strokeLinecap="round" />
          <path d="M98 66q-10-2-12-10 10 0 12 10zM98 64q8-4 12-12-10 2-12 12z" fill="#4a8f1a" />
        </>
      )}
      {items.includes('question') && (
        <>
          <circle cx="122" cy="58" r="10" fill="#fff" stroke={GREEN} strokeWidth="3" />
          <path d="M118.5 55.5a3.5 3.5 0 1 1 5 3.2c-1 .5-1.5 1.2-1.5 2.3M122 64.5v.5" stroke="#4a8f1a" strokeWidth="2.6" fill="none" strokeLinecap="round" />
        </>
      )}
      {items.includes('star') && <path d="M124 52l3 7 7 1-5 5 1 7-6-4-6 4 1-7-5-5 7-1z" fill="#ffd34d" />}
    </>
  )
}

export function Logo({ size = 32, variant = BUDDY_VARIANT }: { size?: number; variant?: string }) {
  const v = BUDDY_VARIANTS[variant] ?? BUDDY_VARIANTS.E2!
  const detailed = size >= 36
  return (
    <svg className="ui-logo" width={size} height={size} viewBox="0 0 140 140" aria-hidden="true" focusable="false">
      <rect width="140" height="140" rx="32" fill={detailed ? '#eef6ff' : 'var(--color-accent-soft, #e8f2ff)'} />
      <path d={BIG} fill={BLUE} />
      {detailed ? (
        <BigFace face={v.big} />
      ) : (
        <>
          <circle cx="42" cy="60" r="7" fill="#fff" />
          <circle cx="66" cy="60" r="7" fill="#fff" />
          <circle cx="43" cy="61" r="4" fill={INK} />
          <circle cx="67" cy="61" r="4" fill={INK} />
        </>
      )}
      <path d={SMALL} fill={GREEN} />
      {detailed ? (
        <SmallFace face={v.small} />
      ) : (
        <>
          <circle cx="91" cy="92" r="4.5" fill="#fff" />
          <circle cx="107" cy="92" r="4.5" fill="#fff" />
          <circle cx="91" cy="93" r="2.5" fill={INK} />
          <circle cx="107" cy="93" r="2.5" fill={INK} />
        </>
      )}
      {detailed && <Accessories items={v.accessories} />}
    </svg>
  )
}
