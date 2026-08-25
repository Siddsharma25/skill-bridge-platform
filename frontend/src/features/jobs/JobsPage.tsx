import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { fetchJobs } from "@/features/jobs/api";

export function JobsPage() {
  const { data: jobs, isLoading } = useQuery({ queryKey: ["jobs"], queryFn: fetchJobs });

  return (
    <div className="mx-auto grid max-w-2xl gap-6 p-8">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Job postings</h1>
        <Button asChild>
          <Link to="/jobs/new">New job</Link>
        </Button>
      </div>

      {isLoading && <p className="text-muted-foreground text-sm">Loading...</p>}
      {jobs && jobs.length === 0 && (
        <p className="text-muted-foreground text-sm">No jobs posted yet.</p>
      )}

      <div className="grid gap-4">
        {jobs?.map((job) => (
          <Link key={job.id} to={`/jobs/${job.id}`}>
            <Card className="hover:bg-accent/50 transition-colors">
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
                <span className="text-muted-foreground ml-auto">
                  {job.matches.length} match{job.matches.length === 1 ? "" : "es"}
                </span>
              </CardContent>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  );
}
