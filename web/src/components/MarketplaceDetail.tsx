import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Package, Star, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { api, type MarketAsset, type MarketEntry, type MarketRating, type MarketReview, type MarketReviews } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { categoryName } from "@/components/MarketplaceWords";
import { cn, messageOf, shortDate } from "@/lib/utils";

const MAX_TEXT = 2000;
const MAX_NAME = 60;

/** Through Home Control, so the browser never talks to the marketplace. The hash keeps a cached image from going stale. */
const assetUrl = (id: string, asset: MarketAsset) =>
  `/api/marketplace/${encodeURIComponent(id)}/assets/${encodeURIComponent(asset.name)}?v=${encodeURIComponent(asset.sha256.slice(0, 16))}`;

export function ListingIcon({ entry, className }: { entry: MarketEntry; className?: string }) {
  const [broken, setBroken] = useState(false);
  const box = cn("shrink-0 rounded-lg", className);
  if (!entry.icon || broken) {
    return <span aria-hidden className={cn(box, "flex items-center justify-center bg-muted text-muted-foreground")}><Package className="size-1/2" /></span>;
  }
  // Decorative: the name is always beside it.
  return <img src={assetUrl(entry.id, entry.icon)} alt="" loading="lazy" className={cn(box, "object-cover")} onError={() => setBroken(true)} />;
}

/** One decimal, in range, whatever the marketplace sent. */
const ratingValue = (value: number) => Math.round(Math.min(5, Math.max(0, value)) * 10) / 10;

/** The stars are drawn for the eye; the label says the same in words. */
function Stars({ value }: { value: number }) {
  const shown = ratingValue(value);
  return (
    <span role="img" aria-label={`${shown} out of 5`} className="inline-flex shrink-0 align-text-bottom">
      {[1, 2, 3, 4, 5].map((n) => (
        <Star key={n} aria-hidden className={cn("size-4", n <= Math.round(shown) ? "fill-current text-warn" : "text-muted-foreground/40")} />
      ))}
    </span>
  );
}

export function RatingLine({ rating }: { rating?: MarketRating }) {
  if (!rating || !(rating.count > 0)) return <p className="text-xs text-muted-foreground">No reviews yet</p>;
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      <Stars value={rating.average} />
      <span aria-hidden className="tabular-nums">{ratingValue(rating.average)}</span>
      <span className="tabular-nums">{rating.count} {rating.count === 1 ? "review" : "reviews"}</span>
    </p>
  );
}

const reviewDate = (value: string) => shortDate(value, "");

/** Whatever a stranger typed, so it stays plain text and nothing in it becomes a link. */
function ReviewItem({ review }: { review: MarketReview }) {
  return (
    <li className="space-y-1 rounded-xl bg-muted/60 p-4">
      <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
        <Stars value={review.rating} />
        <span className="break-all font-medium text-foreground">{review.name || "Anonymous"}</span>
        <span>{reviewDate(review.at)}</span>
      </p>
      {review.text && <p className="whitespace-pre-wrap break-words text-sm">{review.text}</p>}
    </li>
  );
}

function ReviewForm({ id, onPosted }: { id: string; onPosted: () => void }) {
  const [rating, setRating] = useState(0);
  const [text, setText] = useState("");
  const [name, setName] = useState(""); // never the login name: what's posted is public
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.postMarketReview(id, { rating, text, name });
      toast.success("Your review is posted");
      setRating(0);
      setText("");
      setName("");
      onPosted();
    } catch (err) {
      setError(messageOf(err, "That didn't work"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="space-y-3 border-t pt-4" onSubmit={(e) => void submit(e)}>
      <h3 className="text-sm font-medium">Write a review</h3>
      <fieldset className="space-y-1">
        <legend className="text-sm">Your rating</legend>
        <div className="flex flex-wrap gap-1">
          {[1, 2, 3, 4, 5].map((n) => (
            <label key={n} className="flex min-h-11 min-w-11 cursor-pointer items-center justify-center gap-1 rounded-md border px-2 text-sm has-[:checked]:border-primary has-[:checked]:bg-accent has-[:focus-visible]:outline-2 sm:min-h-9">
              <input type="radio" name="rating" value={n} className="sr-only" checked={rating === n} onChange={() => setRating(n)} />
              {n}<Star aria-hidden className="size-3" />
            </label>
          ))}
        </div>
      </fieldset>
      <div className="space-y-1">
        <Label htmlFor="review-text">Your review (optional)</Label>
        <Textarea id="review-text" rows={4} maxLength={MAX_TEXT} value={text} onChange={(e) => setText(e.target.value)} />
      </div>
      <div className="space-y-1">
        <Label htmlFor="review-name">Name to show (optional)</Label>
        <Input id="review-name" maxLength={MAX_NAME} autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      <p className="text-xs text-muted-foreground">
        Your rating, review and name are public and go to the marketplace. Nothing else about your home is sent, apart from an anonymous key that lets you replace this review by posting again. The marketplace also sees your internet address.
      </p>
      {error && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>It wasn't posted.</AlertTitle>
          <AlertDescription className="break-words text-xs">{error}</AlertDescription>
        </Alert>
      )}
      <Button type="submit" size="sm" className="min-h-11 sm:min-h-9" disabled={busy || rating === 0}>{busy ? "Posting…" : "Post publicly"}</Button>
    </form>
  );
}

function Reviews({ entry, canReview, onPosted }: { entry: MarketEntry; canReview: boolean; onPosted: () => void }) {
  const [page, setPage] = useState<MarketReviews | null>(null);
  const [failed, setFailed] = useState(false);
  const [more, setMore] = useState(true); // false once a page came back empty, whatever the count claims
  const [loading, setLoading] = useState(false);

  const load = useCallback(async (offset: number, before: MarketReview[]) => {
    setLoading(true);
    try {
      const next = await api.marketReviews(entry.id, offset);
      const seen = new Set(before.map((r) => r.id));
      setPage({ ...next, reviews: [...before, ...next.reviews.filter((r) => !seen.has(r.id))] });
      setMore(next.reviews.length > 0);
      setFailed(false);
    } catch {
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, [entry.id]);
  useEffect(() => { void load(0, []); }, [load]);

  const rating = page ?? entry.rating;
  const reviews = page?.reviews ?? [];
  return (
    <section className="space-y-3" aria-label="Reviews">
      <div className="space-y-1">
        <h2 className="text-lg font-semibold">Ratings &amp; Reviews</h2>
        <RatingLine rating={rating} />
      </div>
      {failed && <p className="text-sm text-muted-foreground">The marketplace's reviews can't load right now. Everything else here still works.</p>}
      {reviews.length > 0 && <ul className="grid gap-3 sm:grid-cols-2">{reviews.map((r) => <ReviewItem key={r.id} review={r} />)}</ul>}
      {page && more && reviews.length < page.count && (
        <Button variant="outline" size="sm" className="min-h-11 sm:min-h-9" disabled={loading} onClick={() => void load(reviews.length, reviews)}>{loading ? "Loading…" : "Show more"}</Button>
      )}
      {canReview && <ReviewForm id={entry.id} onPosted={() => { void load(0, []); onPosted(); }} />}
    </section>
  );
}

/** One cell of the strip under the title: a small label, a big value, a line under it. */
function Stat({ label, value, children }: { label: string; value: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex min-w-28 flex-col items-center gap-0.5 px-4 text-center">
      <dt className="text-[0.6875rem] font-medium uppercase tracking-wide text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold tabular-nums text-muted-foreground">{value}</dd>
      {children && <dd className="text-xs text-muted-foreground">{children}</dd>}
    </div>
  );
}

const compact = new Intl.NumberFormat(undefined, { notation: "compact" });

/** One listing on its own, laid out like a store page. action is its pill; children are what can be checked about it. */
export function MarketplaceDetail({ entry, canReview, onPosted, action, children }: { entry: MarketEntry; canReview: boolean; onPosted: () => void; action: ReactNode; children: ReactNode }) {
  const rated = (entry.rating?.count ?? 0) > 0;
  return (
    <article className="space-y-8 text-sm" data-testid={`market-detail-${entry.id}`}>
      <header className="flex items-start gap-4 sm:gap-6">
        <ListingIcon entry={entry} className="size-24 rounded-[22%] shadow-sm ring-1 ring-border sm:size-32" />
        <div className="flex min-w-0 flex-col gap-1 self-stretch">
          <h2 className="break-words text-2xl font-semibold leading-tight">{entry.name}</h2>
          <p className="break-words text-base text-muted-foreground">{entry.description}</p>
          <p className="text-sm text-primary">{entry.author}</p>
          <div className="mt-auto pt-3">{action}</div>
        </div>
      </header>
      <dl className="flex divide-x overflow-x-auto border-y py-3">
        <Stat label={rated ? `${entry.rating!.count} ${entry.rating!.count === 1 ? "rating" : "ratings"}` : "Ratings"} value={rated ? ratingValue(entry.rating!.average).toFixed(1) : "—"}>
          {rated ? <Stars value={entry.rating!.average} /> : "Not enough yet"}
        </Stat>
        <Stat label="Downloads" value={compact.format(entry.downloads ?? 0)} />
        <Stat label="Version" value={entry.version} />
        {entry.categories?.[0] && <Stat label="Category" value={<span className="text-base">{categoryName(entry.categories[0])}</span>} />}
      </dl>
      {entry.screenshots?.length ? (
        <section className="space-y-3" aria-label="Screenshots">
          <h2 className="text-lg font-semibold">Screenshots</h2>
          <div className="flex snap-x gap-3 overflow-x-auto pb-1">
            {entry.screenshots.map((shot, n) => (
              <img
                key={shot.name}
                src={assetUrl(entry.id, shot)}
                alt={`Screenshot ${n + 1} of ${entry.name}`}
                loading="lazy"
                className="max-h-80 max-w-[85%] shrink-0 snap-start rounded-2xl border object-contain"
              />
            ))}
          </div>
        </section>
      ) : null}
      {children}
      <Reviews entry={entry} canReview={canReview} onPosted={onPosted} />
    </article>
  );
}
