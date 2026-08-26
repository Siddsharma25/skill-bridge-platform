import { useEffect } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Sparkles } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchJob } from "@/features/jobs/api";
import { recordJobView } from "@/lib/storage/recentlyViewedJobs";
import { chipColor, cn } from "@/lib/utils";

export function JobDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data: job, isLoading, isError } = useQuery({
    queryKey: ["job", id],
    queryFn: () => fetchJob(id!),
    enabled: !!id,
  });

  // Records the view once the job's title is actually known — sessionStorage,
  // not a GraphQL mutation, since "what did I browse" has no reason to be
  // server state (see recentlyViewedJobs.ts).
  useEffect(() => {
    if (job) recordJobView({ id: job.id, title: job.title });
  }, [job]);

  return (
    <div className="mx-auto grid max-w-2xl gap-6 p-8">
      <Link
        to="/jobs"
        className="text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-sm transition-colors"
      >
        <ArrowLeft className="size-3.5" /> Back to jobs
      </Link>

      {isLoading && (
        <div className="grid gap-4">
          <div className="skeleton h-32 rounded-xl" />
          <div className="skeleton h-24 rounded-xl" />
        </div>
      )}
      {isError && <p className="text-destructive text-sm">Couldn't load this job.</p>}
      {job === null && <p className="text-muted-foreground text-sm">Job not found.</p>}

      {job && (
        <>
          <Card className="animate-in-up">
            <CardHeader>
              <CardTitle className="text-xl">{job.title}</CardTitle>
              <CardDescription>{job.description}</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap gap-2 text-sm">
              {job.requiredSkills.map((s) => (
                <span
                  key={s.id}
                  className={cn("rounded-full px-2.5 py-0.5 text-xs font-medium", chipColor(s.id))}
                >
                  {s.name}
                </span>
              ))}
            </CardContent>
          </Card>

          <Card className="animate-in-up" style={{ animationDelay: "60ms" }}>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Sparkles className="size-4 text-fuchsia-500" /> Matches
              </CardTitle>
              <CardDescription>
                Produced by the matching worker off consumed Kafka events — empty if Kafka isn't
                running on this instance.
              </CardDescription>
            </CardHeader>
            <CardContent>
              {job.matches.length === 0 ? (
                <p className="text-muted-foreground text-sm">No matches yet.</p>
              ) : (
                <ul className="grid gap-3 text-sm">
                  {job.matches.map((m) => (
                    <li key={m.userId} className="grid gap-1">
                      <div className="flex items-center justify-between">
                        <span className="font-medium">{m.userId}</span>
                        <span className="text-muted-foreground text-xs">
                          {(m.score * 100).toFixed(0)}% · {new Date(m.matchedAt).toLocaleString()}
                        </span>
                      </div>
                      <div className="bg-secondary h-1.5 w-full overflow-hidden rounded-full">
                        <div
                          className="h-full rounded-full bg-gradient-to-r from-fuchsia-500 via-violet-500 to-sky-500 transition-all duration-500"
                          style={{ width: `${Math.round(m.score * 100)}%` }}
                        />
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
