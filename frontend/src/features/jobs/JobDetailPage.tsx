import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchJob } from "@/features/jobs/api";

export function JobDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data: job, isLoading, isError } = useQuery({
    queryKey: ["job", id],
    queryFn: () => fetchJob(id!),
    enabled: !!id,
  });

  return (
    <div className="mx-auto grid max-w-2xl gap-6 p-8">
      <Link to="/jobs" className="text-muted-foreground text-sm underline underline-offset-4">
        &larr; Back to jobs
      </Link>

      {isLoading && <p className="text-muted-foreground text-sm">Loading...</p>}
      {isError && <p className="text-destructive text-sm">Couldn't load this job.</p>}
      {job === null && <p className="text-muted-foreground text-sm">Job not found.</p>}

      {job && (
        <>
          <Card>
            <CardHeader>
              <CardTitle>{job.title}</CardTitle>
              <CardDescription>{job.description}</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap gap-2 text-sm">
              {job.requiredSkills.map((s) => (
                <span key={s.id} className="bg-secondary rounded-md px-2 py-0.5">
                  {s.name}
                </span>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Matches</CardTitle>
              <CardDescription>
                Produced by the matching worker off consumed Kafka events — empty if Kafka isn't
                running on this instance.
              </CardDescription>
            </CardHeader>
            <CardContent>
              {job.matches.length === 0 ? (
                <p className="text-muted-foreground text-sm">No matches yet.</p>
              ) : (
                <ul className="grid gap-2 text-sm">
                  {job.matches.map((m) => (
                    <li key={m.userId} className="flex items-center justify-between">
                      <span>{m.userId}</span>
                      <span className="text-muted-foreground">
                        {(m.score * 100).toFixed(0)}% · {new Date(m.matchedAt).toLocaleString()}
                      </span>
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
